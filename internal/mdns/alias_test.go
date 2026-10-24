package mdns

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestAliasResponseIsAUniqueRecordPerIPv4(t *testing.T) {
	addrs := []netip.Addr{netip.MustParseAddr("192.0.2.63"), netip.MustParseAddr("fd00::1")}
	msg := aliasResponse("jaggery.local.", addrs, nil, aliasTTL)

	if len(msg.Answer) != 1 {
		t.Fatalf("got %d answers, want 1 (the v6 address has no A record)", len(msg.Answer))
	}
	a := msg.Answer[0].(*dns.A)
	if a.Hdr.Name != "jaggery.local." || a.A.String() != "192.0.2.63" || a.Hdr.Ttl != aliasTTL {
		t.Errorf("got %v", a)
	}
	if a.Hdr.Class&cacheFlush == 0 {
		t.Error("cache-flush bit not set: a phone would keep the previous holder's address")
	}
	if a.Hdr.Class&^cacheFlush != dns.ClassINET {
		t.Errorf("class = %d, want IN with cache-flush", a.Hdr.Class)
	}
}

func TestAliasGoodbyeHasZeroTTL(t *testing.T) {
	msg := aliasResponse("ai.local.", []netip.Addr{netip.MustParseAddr("192.0.2.63")}, nil, 0)
	if msg.Answer[0].Header().Ttl != 0 {
		t.Error("goodbye must carry TTL 0")
	}
}

// The response must survive the wire: the cache-flush bit is part of the
// class field, and a client reads it back as such.
func TestAliasResponseRoundTripsOverTheWire(t *testing.T) {
	raw, err := aliasResponse("ai.local.", []netip.Addr{netip.MustParseAddr("192.0.2.63")}, nil, aliasTTL).Pack()
	if err != nil {
		t.Fatal(err)
	}
	var got dns.Msg
	if err := got.Unpack(raw); err != nil {
		t.Fatal(err)
	}
	if a, ok := got.Answer[0].(*dns.A); !ok || a.A.String() != "192.0.2.63" {
		t.Fatalf("got %v", got.Answer)
	}
}

func TestAliasAnswersOnlyAddressQuestionsForItsName(t *testing.T) {
	cases := []struct {
		q    dns.Question
		want bool
	}{
		{dns.Question{Name: "jaggery.local.", Qtype: dns.TypeA}, true},
		{dns.Question{Name: "JAGGERY.local.", Qtype: dns.TypeA}, true},
		{dns.Question{Name: "jaggery.local.", Qtype: dns.TypeANY}, true},
		{dns.Question{Name: "jaggery.local.", Qtype: dns.TypeAAAA}, true}, // answered with NSEC
		{dns.Question{Name: "jaggery.local.", Qtype: dns.TypeTXT}, false},
		{dns.Question{Name: "ai.local.", Qtype: dns.TypeA}, false},
	}
	for _, c := range cases {
		if got := aliasAsked(c.q, "jaggery.local."); got != c.want {
			t.Errorf("%v: got %v, want %v", c.q, got, c.want)
		}
	}
}

// The NSEC must say "A only", or macOS keeps waiting for an IPv6 answer.
func TestAliasResponseDeniesIPv6WithNSEC(t *testing.T) {
	msg := aliasResponse("ai.local.", []netip.Addr{netip.MustParseAddr("192.0.2.63")}, nil, aliasTTL)
	if len(msg.Extra) != 1 {
		t.Fatalf("extra = %v, want one NSEC", msg.Extra)
	}
	nsec, ok := msg.Extra[0].(*dns.NSEC)
	if !ok {
		t.Fatalf("extra is %T, want NSEC", msg.Extra[0])
	}
	if nsec.Hdr.Name != "ai.local." || nsec.NextDomain != "ai.local." {
		t.Errorf("nsec names = %q / %q", nsec.Hdr.Name, nsec.NextDomain)
	}
	if len(nsec.TypeBitMap) != 1 || nsec.TypeBitMap[0] != dns.TypeA {
		t.Errorf("type bitmap = %v, want [A]", nsec.TypeBitMap)
	}
}

func TestRivalClaimIgnoresOurOwnLoopedBackAnnouncement(t *testing.T) {
	ours := []netip.Addr{netip.MustParseAddr("192.0.2.63")}
	if rivalClaims(aliasResponse("ai.local.", ours, nil, aliasTTL), "ai.local.", ours) {
		t.Fatal("our own announcement counted as a rival")
	}
}

func TestRivalClaimSeesAnotherMachine(t *testing.T) {
	ours := []netip.Addr{netip.MustParseAddr("192.0.2.63")}
	theirs := []netip.Addr{netip.MustParseAddr("192.0.2.55")}

	if !rivalClaims(aliasResponse("ai.local.", theirs, nil, aliasTTL), "ai.local.", ours) {
		t.Error("another machine's claim not seen")
	}
	if rivalClaims(aliasResponse("jaggery.local.", theirs, nil, aliasTTL), "ai.local.", ours) {
		t.Error("a claim for a different name counted")
	}
}

// A goodbye is not a claim, and must not be read as "the name is free" either:
// during a handover it arrives while the new holder is claiming.
func TestRivalGoodbyeIsNotAClaim(t *testing.T) {
	ours := []netip.Addr{netip.MustParseAddr("192.0.2.63")}
	theirs := []netip.Addr{netip.MustParseAddr("192.0.2.65")}
	if rivalClaims(aliasResponse("ai.local.", theirs, nil, 0), "ai.local.", ours) {
		t.Error("goodbye counted as a claim")
	}
}

// R5: with confirmed IPv6, the answer carries AAAA records too, and the NSEC
// lists both types; without, it stays IPv4 only.
func TestAliasResponseCarriesIPv6WhenGiven(t *testing.T) {
	v4 := []netip.Addr{netip.MustParseAddr("192.0.2.63")}
	v6 := []netip.Addr{netip.MustParseAddr("fe80::dea6:32ff:fe9c:3f53")}
	msg := aliasResponse("jaggery.local.", v4, v6, aliasTTL)
	var a, aaaa int
	for _, rr := range msg.Answer {
		switch rr.(type) {
		case *dns.A:
			a++
		case *dns.AAAA:
			aaaa++
		}
	}
	if a != 1 || aaaa != 1 {
		t.Fatalf("answer has %d A and %d AAAA, want 1 and 1", a, aaaa)
	}
	if nsec := msg.Extra[0].(*dns.NSEC); len(nsec.TypeBitMap) != 2 {
		t.Fatalf("NSEC lists %v, want A and AAAA", nsec.TypeBitMap)
	}
	for _, rr := range aliasResponse("jaggery.local.", v4, nil, aliasTTL).Answer {
		if _, ok := rr.(*dns.AAAA); ok {
			t.Fatal("an IPv6 address was published without being confirmed")
		}
	}
}

func TestWatchClaimsReportsOtherAddressesForTheNames(t *testing.T) {
	c := &Conn{handlers: map[int]handler{}}
	ours := []netip.Addr{netip.MustParseAddr("192.0.2.10")}
	type claim struct {
		name string
		addr netip.Addr
	}
	got := make(chan claim, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.WatchClaims(ctx, []string{"jaggery.local", "ai.local"}, ours, func(n string, a netip.Addr) { got <- claim{n, a} })
		close(done)
	}()
	for len(c.handlerList()) == 0 {
		time.Sleep(time.Millisecond)
	}
	rogue := []netip.Addr{netip.MustParseAddr("192.0.2.50")}
	for _, msg := range []*dns.Msg{
		aliasResponse("ai.local.", ours, nil, aliasTTL),       // our own, looped back
		aliasResponse("ai.local.", rogue, nil, 0),             // a goodbye
		aliasResponse("printer.local.", rogue, nil, aliasTTL), // another name
		aliasResponse("ai.local.", rogue, nil, aliasTTL),      // the claim
	} {
		for _, h := range c.handlerList() {
			h(msg, rogue[0])
		}
	}
	select {
	case cl := <-got:
		if cl.name != "ai.local." || cl.addr != rogue[0] {
			t.Fatalf("claim %+v", cl)
		}
	case <-time.After(time.Second):
		t.Fatal("the claim wasn't reported")
	}
	if len(got) != 0 {
		t.Fatalf("%d more claims reported", len(got))
	}
	cancel()
	<-done
	if len(c.handlerList()) != 0 {
		t.Fatal("the watcher's handler stayed after ctx ended")
	}
}

func (c *Conn) handlerList() []handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []handler
	for _, h := range c.handlers {
		out = append(out, h)
	}
	return out
}
