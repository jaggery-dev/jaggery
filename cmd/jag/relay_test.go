package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/update"
)

func TestHouseAssetsCoversOlderAgents(t *testing.T) {
	snap := house.Snapshot{
		Self: house.Machine{Inventory: inventory.Report{Asset: "jag-linux-amd64", OS: "linux", Arch: "amd64"}},
		Peers: []house.Machine{
			{Inventory: inventory.Report{OS: "linux", Arch: "arm"}}, // a Pi on a build that doesn't say
			{Inventory: inventory.Report{Asset: "jag-darwin-arm64"}},
			{}, // never reached: no inventory
		},
	}
	got := houseAssets(snap)
	want := []string{"jag-darwin-arm64", "jag-linux-amd64", "jag-linux-armv7"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A machine relays only for peers a person approved on it; vouched trust
// (C1) must not become the power to run code here.
func TestRelayTrustNeedsDirectApproval(t *testing.T) {
	addrs := []netip.Addr{netip.MustParseAddr("192.0.2.65")}
	key := make([]byte, 32)
	direct := home.Peer{Trust: home.Approved, PublicKey: key, Addrs: addrs}
	vouched := direct
	vouched.ApprovedVia = "pi"
	pending := direct
	pending.Trust = home.Pending
	if !relayTrusted(direct) || relayTrusted(vouched) || relayTrusted(pending) {
		t.Fatalf("direct %v, vouched %v, pending %v", relayTrusted(direct), relayTrusted(vouched), relayTrusted(pending))
	}
}

// githubWith serves one beta release with the given builds, as GitHub does.
func githubWith(t *testing.T, builds map[string][]byte) update.Source {
	t.Helper()
	sums := ""
	for name, data := range builds {
		sum := sha256.Sum256(data)
		sums += fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	files := map[string][]byte{update.SumsName: []byte(sums)}
	m := update.Manifest{Version: "beta.9", Channel: "beta", Commit: "c9", BuiltAt: time.Now().UTC().Truncate(time.Second)}
	files[update.ManifestName], _ = json.Marshal(m)
	files[update.SigName] = signedBy(t, m, []byte(sums))
	for name, data := range builds {
		files[name] = data
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/releases" {
			var assets []map[string]string
			for name := range files {
				assets = append(assets, map[string]string{"name": name, "url": srv.URL + "/a/" + name})
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "beta-9", "prerelease": true, "created_at": time.Now(), "assets": assets}})
			return
		}
		data, ok := files[r.URL.Path[len("/a/"):]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return update.Source{Repo: "o/r", API: srv.URL, HTTP: srv.Client()}
}

// signedBy signs a release with a key trusted for this test.
func signedBy(t *testing.T, m update.Manifest, sums []byte) []byte {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	update.TrustForTest(t, pub)
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, update.Statement(m, sums))))
}

// The relaying machine keeps the builds its house runs and serves them; a
// peer gets the release, the checksums, and only files the release lists.
func TestKeptReleaseIsServedToPeers(t *testing.T) {
	src := githubWith(t, map[string][]byte{
		"jag-linux-amd64":       []byte("amd64 build"),
		"jag-linux-armv7":       []byte("armv7 build"),
		"jag-windows-amd64.exe": []byte("not in this house"),
	})
	rel, err := src.Latest(context.Background(), "beta")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	snap := house.Snapshot{
		Self:  house.Machine{Inventory: inventory.Report{Asset: "jag-linux-amd64"}},
		Peers: []house.Machine{{Inventory: inventory.Report{Asset: "jag-linux-armv7"}}},
	}
	if err := cacheForHouse(context.Background(), src, rel, snap, dir); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	relayHandlers(mux, dir)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + relayReleasePath)
	if err != nil {
		t.Fatal(err)
	}
	var r relayed
	_ = json.NewDecoder(resp.Body).Decode(&r)
	resp.Body.Close()
	if r.Manifest.Version != "beta.9" || r.Manifest.Channel != "beta" {
		t.Fatalf("offered %+v", r.Manifest)
	}
	if err := r.verify(); err != nil {
		t.Fatalf("the relayed release doesn't verify: %v", err)
	}

	get := func(name string) (int, []byte) {
		resp, err := http.Get(srv.URL + relayAssetPath + name)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}
	if _, offered := update.ParseSums(r.Sums)["jag-windows-amd64.exe"]; offered {
		t.Error("offered a build it doesn't keep")
	}
	if _, offered := update.ParseSums(r.Sums)["jag-linux-armv7"]; !offered {
		t.Error("didn't offer a build it keeps")
	}
	code, body := get("jag-linux-armv7")
	if code != http.StatusOK || update.CheckSum(r.SignedSums, "jag-linux-armv7", body) != nil {
		t.Fatalf("armv7 build: %d, %q", code, body)
	}
	if code, _ := get("jag-windows-amd64.exe"); code != http.StatusNotFound {
		t.Errorf("served a build no machine here runs: %d", code)
	}
	for _, name := range []string{"release.json", "..%2Fpeers.json", "SHA256SUMS"} {
		if code, _ := get(name); code != http.StatusNotFound {
			t.Errorf("%s: answered %d, want 404 (only listed builds)", name, code)
		}
	}
}

// R1: a relay keeps the newest and the build the first machine runs, and
// serves either on request.
func TestRelayKeepsTheProvenBuildBesideTheNewest(t *testing.T) {
	dir := t.TempDir()
	keep := func(v string) {
		d := filepath.Join(dir, relayDir, "tag-"+v)
		_ = os.MkdirAll(d, 0o700)
		m, _ := json.Marshal(update.Manifest{Version: v, Channel: "beta"})
		_ = os.WriteFile(filepath.Join(d, update.ManifestName), m, 0o600)
		data := []byte("build " + v)
		sum := sha256.Sum256(data)
		_ = os.WriteFile(filepath.Join(d, update.SumsName), []byte(hex.EncodeToString(sum[:])+"  jag-linux-arm64\n"), 0o600)
		_ = os.WriteFile(filepath.Join(d, "jag-linux-arm64"), data, 0o600)
	}
	for _, v := range []string{"0.5.0-beta.16", "0.5.0-beta.18", "0.5.0-beta.20"} {
		keep(v)
	}
	snap := house.Snapshot{Self: house.Machine{MachineID: "aaa", Self: true, Reachable: true,
		Inventory: inventory.Report{Build: "0.5.0-beta.18"}}}
	pruneRelayCache(dir, snap, "0.5.0-beta.20")

	var versions []string
	for _, k := range keptReleases(dir) {
		versions = append(versions, k.Manifest.Version)
	}
	if fmt.Sprint(versions) != "[0.5.0-beta.20 0.5.0-beta.18]" {
		t.Fatalf("kept %v, want the newest and the holder's", versions)
	}
	if r, _, ok := cachedRelease(dir, ""); !ok || r.Manifest.Version != "0.5.0-beta.20" {
		t.Fatalf("default offer %+v", r.Manifest)
	}
	if r, _, ok := cachedRelease(dir, "0.5.0-beta.18"); !ok || r.Manifest.Version != "0.5.0-beta.18" {
		t.Fatalf("asked for the proven build, got %+v", r.Manifest)
	}

	mux := http.NewServeMux()
	relayHandlers(mux, dir)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + relayAssetPath + "jag-linux-arm64" + versionQuery("0.5.0-beta.18"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "build 0.5.0-beta.18" {
		t.Fatalf("served %q", body)
	}
}

// U5: a relay can't change what it hands on. The signature covers the full
// checksums, and the held ones must match them.
func TestARelayedReleaseMustBeSigned(t *testing.T) {
	m := update.Manifest{Version: "0.9.0-beta.2", Channel: "beta", Commit: "c2"}
	full := []byte("aaaa  jag-linux-amd64\nbbbb  jag-linux-arm64\n")
	good := relayed{Manifest: m, Sums: []byte("bbbb  jag-linux-arm64\n"), SignedSums: full, Sig: signedBy(t, m, full)}
	if err := good.verify(); err != nil {
		t.Fatal(err)
	}
	unsigned := good
	unsigned.Sig = nil
	swapped := good
	swapped.Sums = []byte("cccc  jag-linux-arm64\n")
	resigned := good
	resigned.SignedSums = []byte("cccc  jag-linux-arm64\n")
	resigned.Sums = resigned.SignedSums
	older := good // a relay from before U5
	older.SignedSums, older.Sig = nil, nil
	for name, r := range map[string]relayed{"unsigned": unsigned, "held sums swapped": swapped, "signed sums swapped": resigned, "pre-U5 relay": older} {
		if err := r.verify(); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !update.IsSignatureError(err) && !strings.Contains(err.Error(), "isn't the signed one") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A relay doesn't offer a build it knows was withdrawn: a machine that
// updates through it is offered the newest good one instead, and so moves
// off the withdrawn build.
func TestRelaySkipsWithdrawnBuilds(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.7.0-beta.3", "0.7.0-beta.4"} {
		d := filepath.Join(dir, relayDir, "v"+v)
		_ = os.MkdirAll(d, 0o700)
		m, _ := json.Marshal(update.Manifest{Version: v, Channel: "beta", Commit: "c-" + v})
		_ = os.WriteFile(filepath.Join(d, update.ManifestName), m, 0o600)
		_ = os.WriteFile(filepath.Join(d, update.SumsName), []byte{}, 0o600)
	}
	if r, _, _ := cachedRelease(dir, ""); r.Manifest.Version != "0.7.0-beta.4" {
		t.Fatalf("before the withdrawal: offered %q", r.Manifest.Version)
	}
	if err := update.MarkWithdrawn(dir, "c-0.7.0-beta.4"); err != nil {
		t.Fatal(err)
	}
	if r, _, ok := cachedRelease(dir, ""); !ok || r.Manifest.Version != "0.7.0-beta.3" {
		t.Fatalf("after the withdrawal: offered %q, want beta.3", r.Manifest.Version)
	}
	if _, _, ok := cachedRelease(dir, "0.7.0-beta.4"); ok {
		t.Fatal("the withdrawn build is still served when asked for by name")
	}
}
