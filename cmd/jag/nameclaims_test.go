package main

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
)

func TestForeignNameClaims(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr
	peers := []home.Peer{
		{Name: "nas", Trust: home.Approved, Addrs: []netip.Addr{ip("192.0.2.11")}},
		{Name: "desktop", Trust: home.Pending, Addrs: []netip.Addr{ip("192.0.2.12")}},
	}
	l := &claimLog{}
	l.add("ai.local.", ip("192.0.2.11"), now)                   // the house's own holder
	l.add("ai.local.", ip("192.0.2.50"), now.Add(-time.Minute)) // a stranger
	l.add("ai.local.", ip("192.0.2.50"), now)                   // heard again
	l.add("jaggery.local.", ip("192.0.2.12"), now)              // known, not approved
	l.add("jaggery.local.", ip("192.0.2.60"), now.Add(-claimMemory-time.Second))

	got := l.foreign(peers, now)
	if len(got) != 2 {
		t.Fatalf("%d claims: %+v", len(got), got)
	}
	if c := got[0]; c.Name != "ai.local" || c.Addr != ip("192.0.2.50") || !c.FirstSeen.Equal(now.Add(-time.Minute).UTC()) || c.Machine != "" {
		t.Errorf("stranger: %+v", c)
	}
	if c := got[1]; c.Name != "jaggery.local" || c.Machine != "desktop" {
		t.Errorf("unapproved machine: %+v", c)
	}
	if len(l.seen) != 3 {
		t.Errorf("an old claim wasn't forgotten: %d kept", len(l.seen))
	}

	if r := checkNameClaims(house.Snapshot{}); r.state != pass {
		t.Errorf("no claims: %+v", r)
	}
	r := checkNameClaims(house.Snapshot{NameClaims: got})
	if r.state != fail || !strings.Contains(r.detail, "Something at 192.0.2.50 also answers for ai.local") ||
		!strings.Contains(r.detail, "desktop at 192.0.2.12, a Jaggery machine not approved here, also answers for jaggery.local") || r.fix == "" {
		t.Errorf("doctor: %+v", r)
	}
}
