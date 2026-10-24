package main

import (
	"io"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
)

// captureStdout runs f and returns what it printed.
func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	ferr := f()
	w.Close()
	os.Stdout = saved
	out, _ := io.ReadAll(r)
	return string(out), ferr
}

func TestSameNameShownWhereApprovalIsOffered(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	self, _, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	store, err := home.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	real, fake := newIdentity(t, "nas"), newIdentity(t, "NAS")
	if _, err := store.Adopt(home.Observation{MachineID: real.ID, Name: "nas", Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.11")}, Port: 7373},
		real.PublicKey, home.Approved, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Observe(home.Observation{MachineID: fake.ID, Name: "NAS", Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.66")}, Port: 7373}, time.Now()); err != nil {
		t.Fatal(err)
	}
	other := newIdentity(t, "desktop")
	if err := store.Observe(home.Observation{MachineID: other.ID, Name: "desktop", Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.12")}, Port: 7373}, time.Now()); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, listPeers)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`2 machines call themselves "nas":`,
		real.ID + "  at 192.0.2.11:7373, fingerprint " + real.Fingerprint() + " (approved)",
		fake.ID + "  at 192.0.2.66:7373, fingerprint not proven yet (pending)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("jag peers doesn't say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `call themselves "desktop"`) {
		t.Errorf("a name used once was flagged:\n%s", out)
	}

	out, err = captureStdout(t, func() error { return setTrust([]string{fake.ID}, home.Approved) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `2 machines call themselves "NAS":`) || !strings.Contains(out, real.ID) {
		t.Errorf("jag approve doesn't warn:\n%s", out)
	}
	out, _ = captureStdout(t, func() error { return setTrust([]string{other.ID}, home.Approved) })
	if strings.Contains(out, "call themselves") {
		t.Errorf("jag approve warned about a name used once:\n%s", out)
	}

	// A peer using this machine's own name.
	impostor := newIdentity(t, self.Name)
	if err := store.Observe(home.Observation{MachineID: impostor.ID, Name: self.Name}, time.Now()); err != nil {
		t.Fatal(err)
	}
	peers, _ := store.List()
	g := sharesName(sameNames(self, peers), impostor.ID)
	if len(g) != 2 || g[0].Trust != "this machine" || g[0].Fingerprint != self.Fingerprint() {
		t.Errorf("a peer with this machine's name: %+v", g)
	}

	jsonOutput = true
	defer func() { jsonOutput = false }()
	out, _ = captureStdout(t, listPeers)
	if strings.Count(out, `"same_name": true`) != 3 {
		t.Errorf("jag peers --json marks %d machines, want 3:\n%s", strings.Count(out, `"same_name": true`), out)
	}
}
