package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/shares"
)

// Shared folders across the house (tech-specs/shared-folders.md).
// A share is served by the machine that holds it. Any machine's /mcp takes a
// call, finds the holder from the house snapshot, and either runs it here or
// sends it to the holder over mutual TLS, where it is checked again against
// the holder's own shares.json.

const filesPath = "/v1/files"

// houseShare is a share somewhere in the house, as models and agents see it.
type houseShare struct {
	Name      string // as shown: "docs", or "docs@nas" when two machines have a docs
	Share     string // the holder's own name for it
	Machine   string
	MachineID string
	Self      bool
	Writable  bool
	Online    bool
}

// houseShares lists every share in the house: this machine's from its
// shares.json, the others' from their last inventory.
func houseShares(dir string, snap house.Snapshot) []houseShare {
	var out []houseShare
	self := snap.Self.Name
	if self == "" {
		if id, err := identity.Load(); err == nil {
			self = id.Name
		}
	}
	local, _ := shares.Load(dir)
	for _, s := range local {
		out = append(out, houseShare{Share: s.Name, Machine: self, MachineID: snap.Self.MachineID, Self: true, Writable: s.Writable, Online: true})
	}
	for _, m := range snap.Peers {
		if m.Trust != home.Approved || m.Incompatible {
			continue
		}
		for _, s := range m.Inventory.Shares {
			out = append(out, houseShare{Share: s.Name, Machine: m.Name, MachineID: m.MachineID, Writable: s.Writable, Online: m.Reachable})
		}
	}
	count := map[string]int{}
	for _, s := range out {
		count[s.Share]++
	}
	for i := range out {
		out[i].Name = out[i].Share
		if count[out[i].Share] > 1 {
			out[i].Name = out[i].Share + "@" + strings.ToLower(out[i].Machine)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// findShare resolves "docs" or "docs@nas".
func findShare(list []houseShare, name string) (houseShare, error) {
	want, machine, qualified := strings.Cut(name, "@")
	var found []houseShare
	for _, s := range list {
		if s.Share == want && (!qualified || strings.EqualFold(s.Machine, machine)) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return houseShare{}, fmt.Errorf("no share named %q in the house (list_shares shows them)", name)
	}
	var names []string
	for _, s := range found {
		names = append(names, s.Name)
	}
	return houseShare{}, fmt.Errorf("%q is on more than one machine: use %s", name, strings.Join(names, " or "))
}

// fileCaller says who asked, for the access log (S3).
type fileCaller struct {
	App      string // the MCP client's name from its handshake
	From     string // the address the call came from
	Via      string // the machine that took the call, when it isn't this one
	ServedBy string // the machine that served it, when it isn't this one
}

// doFiles runs a share operation wherever the share lives.
func doFiles(ctx context.Context, id *identity.Identity, store *home.Store, req shares.Request, who fileCaller) (shares.Response, houseShare, error) {
	dir, err := identity.StateDir()
	if err != nil {
		return shares.Response{}, houseShare{}, err
	}
	hs, err := findShare(houseShares(dir, loadHouse()), req.Share)
	if err != nil {
		return shares.Response{}, hs, err
	}
	req.Share = hs.Share
	var resp shares.Response
	if hs.Self {
		resp, err = shares.Do(ctx, dir, req, time.Now())
	} else if !hs.Online {
		err = fmt.Errorf("%s, which holds %q, is offline", hs.Machine, hs.Name)
	} else {
		resp, err = filesOnPeer(ctx, id, store, hs.MachineID, req, who)
		who.ServedBy = hs.Machine
	}
	logFileCall(dir, req, resp, err, who)
	return resp, hs, err
}

// fileRequest is what one machine sends another: the operation and who
// asked, for the holder's log.
type fileRequest struct {
	shares.Request
	App  string `json:"app,omitempty"`
	From string `json:"from,omitempty"`
}

func filesOnPeer(ctx context.Context, id *identity.Identity, store *home.Store, machineID string, req shares.Request, who fileCaller) (shares.Response, error) {
	if id == nil || store == nil {
		return shares.Response{}, errors.New("no peer connection here")
	}
	peer, err := store.Get(machineID)
	if err != nil {
		return shares.Response{}, err
	}
	if peer.Trust != home.Approved || !peer.Verified() || len(peer.Addrs) == 0 {
		return shares.Response{}, fmt.Errorf("%s is not a reachable approved peer", peer.Name)
	}
	client, err := peerHTTPClient(id, ed25519.PublicKey(peer.PublicKey), 30*time.Second)
	if err != nil {
		return shares.Response{}, err
	}
	body, err := json.Marshal(fileRequest{Request: req, App: who.App, From: who.From})
	if err != nil {
		return shares.Response{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+peerFilesAddr(peer)+filesPath, bytes.NewReader(body))
	if err != nil {
		return shares.Response{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(hreq)
	if err != nil {
		return shares.Response{}, fmt.Errorf("reaching %s: %w", peer.Name, err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusNotFound && resp.Header.Get("Content-Type") != "application/json" {
		return shares.Response{}, fmt.Errorf("%s runs an older Jaggery without shared folders", peer.Name)
	}
	var out struct {
		shares.Response
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return shares.Response{}, fmt.Errorf("reading %s's answer: %w", peer.Name, err)
	}
	if out.Error != "" {
		return shares.Response{}, errors.New(out.Error)
	}
	return out.Response, nil
}

// peerFilesAddr is where a peer's secured listener is; tests point it at a
// test server.
var peerFilesAddr = func(p home.Peer) string {
	return net.JoinHostPort(p.Addrs[0].String(), strconv.Itoa(discovery.SecurePort))
}

// filesHandler is the holder's end, on the secured listener: only approved
// peers reach it. The holder checks the request against its own shares.
func filesHandler(stateDir func() (string, error), peerName func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req fileRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, shares.MaxWrite*2)).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "reading request: " + err.Error()})
			return
		}
		dir, err := stateDir()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		resp, err := shares.Do(r.Context(), dir, req.Request, time.Now())
		logFileCall(dir, req.Request, resp, err, fileCaller{App: req.App, From: req.From, Via: peerName(r)})
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// logFileCall records a call in this machine's access log, and writes a
// line to the journal.
func logFileCall(dir string, req shares.Request, resp shares.Response, err error, who fileCaller) {
	e := recordFileCall(dir, req, resp, err, who)
	fmt.Fprintln(os.Stderr, "files: "+logLine(e))
}

// recordFileCall records a call in this machine's access log.
func recordFileCall(dir string, req shares.Request, resp shares.Response, err error, who fileCaller) shares.LogEntry {
	e := shares.LogEntry{
		Time: time.Now(), App: who.App, From: who.From, Via: who.Via, ServedBy: who.ServedBy,
		Op: req.Op, Share: req.Share, Path: req.Path, Query: req.Query,
		Matches: len(resp.Matches), OldSHA256: resp.OldSHA256, NewSHA256: resp.NewSHA256, Version: resp.Version,
	}
	switch req.Op {
	case shares.OpRead:
		e.Bytes = int64(len(resp.Data))
	case shares.OpWrite, shares.OpRestore:
		e.Bytes = resp.Written
	}
	if resp.Path != "" {
		e.Path = resp.Path
	}
	if err != nil {
		e.Error = err.Error()
		// The holder's log keeps the operating system's own words too.
		var fe *shares.FileError
		if errors.As(err, &fe) {
			e.Error += " (" + fe.Err.Error() + ")"
		}
	}
	if lerr := shares.AppendLog(dir, e); lerr != nil {
		fmt.Fprintln(os.Stderr, "warning: files log:", lerr)
	}
	return e
}

// logLine is an entry in one line: what, then who.
func logLine(e shares.LogEntry) string {
	what := fmt.Sprintf("%s %s:%s", e.Op, e.Share, e.Path)
	switch {
	case e.Op == shares.OpSearch:
		what = fmt.Sprintf("search %s for %q (%d matches)", e.Share, e.Query, e.Matches)
	case e.Op == shares.OpList && (e.Path == "" || e.Path == "."):
		what = fmt.Sprintf("list %s", e.Share)
	case e.Bytes > 0:
		what += " (" + sizeText(e.Bytes) + ")"
	}
	if e.Error != "" {
		what += " refused: " + e.Error
	}
	app := e.App
	if app == "" {
		app = "an app"
	}
	who := app
	if e.From != "" {
		who += " from " + e.From
	}
	if e.Via != "" {
		who += " via " + e.Via
	}
	if e.ServedBy != "" {
		who += ", served by " + e.ServedBy
	}
	return what + "  [" + who + "]"
}

// peerNameFrom names the approved peer that made a request on the secured
// listener, from the key in its certificate.
func peerNameFrom(store *home.Store) func(*http.Request) string {
	return func(r *http.Request) string {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 || store == nil {
			return "a peer"
		}
		key, ok := r.TLS.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
		if !ok {
			return "a peer"
		}
		peers, _ := store.List()
		for _, p := range peers {
			if bytes.Equal(p.PublicKey, key) {
				return p.Name
			}
		}
		return "a peer"
	}
}
