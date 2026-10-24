package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/pairing"
)

func newIdentity(t *testing.T, name string) *identity.Identity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &identity.Identity{ID: identity.IDFromKey(pub), Name: name, PublicKey: pub, PrivateKey: priv}
}

// inviterServer runs the agent's join handler over real HTTP, with its state
// in a temp dir, and returns where to reach it.
func inviterServer(t *testing.T) (*identity.Identity, *home.Store, string, netip.AddrPort) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	store, err := home.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	inviter := newIdentity(t, "inviter")
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+pairing.JoinPath, joinHandler(inviter, store))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return inviter, store, dir, netip.MustParseAddrPort(srv.Listener.Addr().String())
}

func TestJoinWithCodeApprovesBothWays(t *testing.T) {
	inviter, store, dir, at := inviterServer(t)
	inv, err := pairing.CreateInvite(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	joiner := newIdentity(t, "joiner")

	verified, err := requestJoin(context.Background(), inv.Code, joiner, at.Addr(), int(at.Port()))
	if err != nil {
		t.Fatal(err)
	}
	if verified.MachineID != inviter.ID {
		t.Fatalf("joined %q, want the inviter %q", verified.MachineID, inviter.ID)
	}

	peer, err := store.Get(joiner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if peer.Trust != home.Approved || !peer.Verified() {
		t.Fatalf("inviter stored the joiner as %s (verified %v), want approved with its key", peer.Trust, peer.Verified())
	}

	// One use only.
	if _, err := requestJoin(context.Background(), inv.Code, newIdentity(t, "second"), at.Addr(), int(at.Port())); !errors.Is(err, errNoInviteThere) {
		t.Fatalf("reusing the code: got %v, want errNoInviteThere", err)
	}
}

func TestJoinWithWrongCodeIsRefusedAndCounted(t *testing.T) {
	_, store, dir, at := inviterServer(t)
	if _, err := pairing.CreateInvite(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	joiner := newIdentity(t, "joiner")

	for i := 0; i < pairing.MaxFailures; i++ {
		wrong, _ := pairing.NewCode()
		if _, err := requestJoin(context.Background(), wrong, joiner, at.Addr(), int(at.Port())); !errors.Is(err, pairing.ErrWrongCode) {
			t.Fatalf("attempt %d: got %v, want ErrWrongCode", i+1, err)
		}
	}
	if _, err := store.Get(joiner.ID); err == nil {
		t.Fatal("a joiner with a wrong code was stored")
	}
	if _, err := pairing.LoadInvite(dir, time.Now()); !errors.Is(err, pairing.ErrNoInvite) {
		t.Fatalf("after %d wrong codes the invite should be burned, got %v", pairing.MaxFailures, err)
	}
}

func TestResolveJoinTarget(t *testing.T) {
	ctx := context.Background()
	for in, want := range map[string]joinTarget{
		"198.51.100.7":       {addr: netip.MustParseAddr("198.51.100.7"), port: discovery.AgentPort},
		"198.51.100.7:9000":  {addr: netip.MustParseAddr("198.51.100.7"), port: 9000},
		"[2001:db8::1]:7373": {addr: netip.MustParseAddr("2001:db8::1"), port: 7373},
		"::ffff:192.0.2.5":   {addr: netip.MustParseAddr("192.0.2.5"), port: discovery.AgentPort},
	} {
		got, err := resolveJoinTarget(ctx, in)
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"198.51.100.7:0", "198.51.100.7:http", "no-such-host.invalid"} {
		if _, err := resolveJoinTarget(ctx, bad); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
