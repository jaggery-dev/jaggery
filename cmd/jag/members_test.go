package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/pairing"
)

type node struct {
	id    *identity.Identity
	store *home.Store
}

func newNode(t *testing.T, name string) node {
	t.Helper()
	store, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return node{id: newIdentity(t, name), store: store}
}

// trust records b as approved by a person on a, the way `jag join` does.
func (a node) trust(t *testing.T, b node, addr string) home.Peer {
	t.Helper()
	obs := home.Observation{MachineID: b.id.ID, Name: b.id.Name, Addrs: []netip.Addr{netip.MustParseAddr(addr)}, Port: 7373}
	p, err := a.store.Adopt(obs, b.id.PublicKey, home.Approved, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return *p
}

// roster reads a's roster through its handler and checks it as a peer would.
func (a node) roster(t *testing.T) pairing.Roster {
	t.Helper()
	rec := httptest.NewRecorder()
	rosterHandler(a.id, a.store)(rec, httptest.NewRequest(http.MethodGet, pairing.RosterPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("roster handler answered %d", rec.Code)
	}
	var signed pairing.SignedRoster
	if err := json.NewDecoder(rec.Body).Decode(&signed); err != nil {
		t.Fatal(err)
	}
	r, err := pairing.OpenRoster(signed, a.id.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// House-wide trust: a fourth machine joins through the Pi, and the desktop
// and the laptop trust it, and it them, with nobody pairing them by hand.
func TestJoiningThroughOneMachineJoinsTheHouse(t *testing.T) {
	desktop, laptop, pi, fourth := newNode(t, "desktop"), newNode(t, "laptop"), newNode(t, "pi"), newNode(t, "fourth")
	for _, pair := range [][2]node{{desktop, laptop}, {desktop, pi}, {laptop, pi}} {
		pair[0].trust(t, pair[1], "192.0.2.1")
		pair[1].trust(t, pair[0], "192.0.2.2")
	}
	piAsSeenByDesktop, _ := desktop.store.Get(pi.id.ID)
	piAsSeenByAir, _ := laptop.store.Get(pi.id.ID)

	// The code exchange: the Pi and the new machine approve each other.
	pi.trust(t, fourth, "192.0.2.80")
	piAsSeenByFourth := fourth.trust(t, pi, "192.0.2.63")

	// One scan each.
	adoptRoster(desktop.id, desktop.store, piAsSeenByDesktop, pi.roster(t))
	adoptRoster(laptop.id, laptop.store, piAsSeenByAir, pi.roster(t))
	adoptRoster(fourth.id, fourth.store, piAsSeenByFourth, pi.roster(t))

	for _, n := range []node{desktop, laptop} {
		if !n.store.IsApproved(fourth.id.PublicKey) {
			t.Errorf("%s does not trust the new machine", n.id.Name)
		}
		if p, _ := n.store.Get(fourth.id.ID); p.ApprovedVia != pi.id.ID {
			t.Errorf("%s: new machine approved via %q, want the Pi", n.id.Name, p.ApprovedVia)
		}
	}
	for _, n := range []node{desktop, laptop} {
		if !fourth.store.IsApproved(n.id.PublicKey) {
			t.Errorf("the new machine does not trust %s", n.id.Name)
		}
	}
	if _, err := fourth.store.Get(fourth.id.ID); err == nil {
		t.Error("the new machine stored itself as a peer")
	}
}

// Only approved machines with a proven key are vouched for; a machine merely
// seen on the network is not.
func TestRosterListsOnlyApprovedVerifiedPeers(t *testing.T) {
	pi, desktop := newNode(t, "pi"), newNode(t, "desktop")
	pi.trust(t, desktop, "192.0.2.65")
	stranger := home.Observation{MachineID: "stranger", Name: "tv", Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.99")}, Port: 7373}
	if err := pi.store.Observe(stranger, time.Now()); err != nil {
		t.Fatal(err)
	}
	r := pi.roster(t)
	if len(r.Members) != 1 || r.Members[0].MachineID != desktop.id.ID {
		t.Fatalf("roster lists %+v, want only the desktop", r.Members)
	}
}
