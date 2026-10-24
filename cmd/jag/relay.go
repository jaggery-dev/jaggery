package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/update"
	"github.com/jaggery-dev/jaggery/internal/version"
)

// A machine that can reach the release source keeps the newest release's
// builds for every platform in the house, and serves them to approved peers
// over mutual TLS. A machine that can't reach GitHub (blocked, offline, or
// rate-limited) updates from it.
//
// A relay passes on the release's signature with the full
// SHA256SUMS it covers, and the machine taking a build checks both, so a
// relay can't hand out code of its own. On top of that, a machine still takes
// builds only from peers a person approved on it directly, not from machines
// another machine's roster vouched for (decision 8).

const (
	relayReleasePath = "/v1/update/release"
	relayAssetPath   = "/v1/update/asset/"
	relayDir         = "releases"
)

// relayed is what a relaying machine offers: the release, the checksums of
// the builds it holds, and the signed checksums of every build with their
// signature. Sums stays the held subset, which is what agents before U5 read.
type relayed struct {
	Manifest   update.Manifest `json:"manifest"`
	Sums       []byte          `json:"sums"`
	SignedSums []byte          `json:"signed_sums,omitempty"`
	Sig        []byte          `json:"sig,omitempty"`
}

// cacheForHouse keeps rel's builds for every platform in the house, from src,
// replacing any older release kept before. Each is checked against
// the signed SHA256SUMS on the way in.
func cacheForHouse(ctx context.Context, src update.Source, rel update.Release, snap house.Snapshot, stateDir string) error {
	dir := filepath.Join(stateDir, relayDir, rel.Tag)
	sums := rel.Sums
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, name := range houseAssets(snap) {
		path := filepath.Join(dir, name)
		if data, err := os.ReadFile(path); err == nil && update.CheckSum(sums, name, data) == nil {
			continue // already kept
		}
		if _, ok := update.ParseSums(sums)[name]; !ok {
			continue // the release has no build for that platform
		}
		data, err := src.Download(ctx, rel, name)
		if err != nil {
			return err
		}
		if err := writeFile(path, data); err != nil {
			return err
		}
	}
	manifest, _ := json.Marshal(rel.Manifest)
	if err := writeFile(filepath.Join(dir, update.ManifestName), manifest); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, update.SumsName), sums); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, update.SigName), rel.Sig); err != nil {
		return err
	}
	pruneRelayCache(stateDir, snap, rel.Manifest.Version)
	return nil
}

// keptReleases lists the releases this machine keeps, newest first.
func keptReleases(stateDir string) []keptRelease {
	entries, err := os.ReadDir(filepath.Join(stateDir, relayDir))
	if err != nil {
		return nil
	}
	var out []keptRelease
	for _, e := range entries {
		dir := filepath.Join(stateDir, relayDir, e.Name())
		var r relayed
		raw, err := os.ReadFile(filepath.Join(dir, update.ManifestName))
		if err != nil || json.Unmarshal(raw, &r.Manifest) != nil {
			continue
		}
		sums, err := os.ReadFile(filepath.Join(dir, update.SumsName))
		if err != nil {
			continue
		}
		r.Sums = heldSums(sums, dir)
		r.SignedSums = sums
		r.Sig, _ = os.ReadFile(filepath.Join(dir, update.SigName)) // none: kept before U5
		out = append(out, keptRelease{r, dir})
	}
	sort.Slice(out, func(i, j int) bool { return newerManifest(out[i].Manifest, out[j].Manifest) })
	return out
}

type keptRelease struct {
	relayed
	dir string
}

func newerManifest(a, b update.Manifest) bool {
	if version.Valid(a.Version) && version.Valid(b.Version) {
		return version.Compare(a.Version, b.Version) > 0
	}
	return a.BuiltAt.After(b.BuiltAt)
}

// houseAssets lists the release files the house's machines run.
func houseAssets(snap house.Snapshot) []string {
	seen := map[string]bool{}
	for _, m := range snap.All() {
		inv := m.Inventory
		switch {
		case inv.Asset != "":
			seen[inv.Asset] = true
		case inv.OS != "" && inv.Arch != "":
			// An agent older than U3 doesn't say; ARM boards in the house run v7.
			seen[version.AssetFor(inv.OS, inv.Arch, "7")] = true
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// pruneRelayCache keeps the release just kept, the newest, and the one the
// first machine runs (the rest of the house installs that one, R1).
func pruneRelayCache(stateDir string, snap house.Snapshot, just string) {
	keep := map[string]bool{just: true}
	kept := keptReleases(stateDir)
	if len(kept) > 0 {
		keep[kept[0].Manifest.Version] = true
	}
	for _, m := range snap.All() {
		if m.MachineID == snap.NameHolder() && m.Inventory.Build != "" {
			keep[m.Inventory.Build] = true
		}
	}
	for _, k := range kept {
		if !keep[k.Manifest.Version] {
			_ = os.RemoveAll(k.dir)
		}
	}
}

// cachedRelease is a release this machine keeps, with its directory: the
// version asked for, or the newest when want is empty. Its sums cover only
// the builds actually kept here: a relay offers what it holds, not
// everything the release lists.
//
// A build this machine knows was withdrawn is never offered, so machines
// that update through it move off a withdrawn build too, instead of being
// offered it as the newest (found by the first withdrawal rehearsal).
func cachedRelease(stateDir, want string) (relayed, string, bool) {
	withdrawn := update.Withdrawn(stateDir)
	for _, k := range keptReleases(stateDir) {
		if k.Manifest.Commit != "" && slices.Contains(withdrawn, k.Manifest.Commit) {
			continue
		}
		if want == "" || k.Manifest.Version == want {
			return k.relayed, k.dir, true
		}
	}
	return relayed{}, "", false
}

// relayHandlers serve the kept release on the secured listener.
func relayHandlers(mux *http.ServeMux, stateDir string) {
	mux.HandleFunc("GET "+relayReleasePath, func(w http.ResponseWriter, r *http.Request) {
		rel, _, ok := cachedRelease(stateDir, r.URL.Query().Get("version"))
		if !ok {
			http.Error(w, "no release kept here", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("GET "+relayAssetPath+"{name}", func(w http.ResponseWriter, r *http.Request) {
		rel, dir, ok := cachedRelease(stateDir, r.URL.Query().Get("version"))
		name := r.PathValue("name")
		// Only files the release lists; the name never reaches the filesystem otherwise.
		if _, listed := update.ParseSums(rel.Sums)[name]; !ok || !listed || filepath.Base(name) != name {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, name))
	})
}

// heldSums keeps the SHA256SUMS lines for files present in dir.
func heldSums(sums []byte, dir string) []byte {
	var out []byte
	for _, line := range strings.SplitAfter(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != name {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			out = append(out, line...)
		}
	}
	return out
}

// verify checks the relayed release's signature, and that the checksums of
// the builds the relay holds are the signed ones.
func (r relayed) verify() error {
	if err := update.Verify(r.Manifest, r.SignedSums, r.Sig); err != nil {
		return err
	}
	signed := update.ParseSums(r.SignedSums)
	for name, sum := range update.ParseSums(r.Sums) {
		if signed[name] != sum {
			return fmt.Errorf("%s: the relay's checksum for %s isn't the signed one", r.Manifest.Version, name)
		}
	}
	return nil
}

var errNoRelay = errors.New("no approved peer offers a build for this machine")

// peerRelease finds the newest release on channel that a directly approved,
// reachable peer offers, or exactly version want when it isn't empty.
func peerRelease(ctx context.Context, id *identity.Identity, store *home.Store, channel, want string) (relayed, home.Peer, error) {
	peers, err := store.List()
	if err != nil {
		return relayed{}, home.Peer{}, err
	}
	var (
		best     relayed
		bestPeer home.Peer
		found    bool
	)
	for _, p := range peers {
		if !relayTrusted(p) {
			continue
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var r relayed
		err := peerGet(fetchCtx, id, p, relayReleasePath+versionQuery(want), func(body io.Reader) error {
			return json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&r)
		})
		cancel()
		if err != nil || r.Manifest.Channel != channel || (want != "" && r.Manifest.Version != want) {
			continue
		}
		if _, has := update.ParseSums(r.Sums)[version.Asset()]; !has {
			continue // it doesn't keep this machine's platform (yet)
		}
		if err := r.verify(); err != nil {
			fmt.Printf("refused %s from %s: %v\n", r.Manifest.Version, p.Name, err)
			continue
		}
		if !found || newerManifest(r.Manifest, best.Manifest) {
			best, bestPeer, found = r, p, true
		}
	}
	if !found {
		return relayed{}, home.Peer{}, errNoRelay
	}
	return best, bestPeer, nil
}

// relayTrusted: approved by a person on this machine, with a proven key and
// an address. See the package comment above for why vouched peers don't count.
func relayTrusted(p home.Peer) bool {
	return p.Trust == home.Approved && p.ApprovedVia == "" && p.Verified() && len(p.Addrs) > 0
}

// peerDownload fetches name from a relaying peer and checks it against the
// signed sums that peer passed on (peerRelease verified them).
func peerDownload(ctx context.Context, id *identity.Identity, p home.Peer, r relayed, name string) ([]byte, error) {
	var data []byte
	err := peerGet(ctx, id, p, relayAssetPath+name+versionQuery(r.Manifest.Version), func(body io.Reader) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(body, 200<<20))
		return err
	})
	if err != nil {
		return nil, err
	}
	return data, update.CheckSum(r.SignedSums, name, data)
}

// peerGet makes one mutual-TLS GET to an approved peer.
func peerGet(ctx context.Context, id *identity.Identity, p home.Peer, path string, read func(io.Reader) error) error {
	client, err := peerHTTPClient(id, ed25519.PublicKey(p.PublicKey), 5*time.Minute)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(p.Addrs[0].String(), strconv.Itoa(discovery.SecurePort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", p.Name, resp.Status)
	}
	return read(resp.Body)
}

func versionQuery(v string) string {
	if v == "" {
		return ""
	}
	return "?version=" + url.QueryEscape(v)
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
