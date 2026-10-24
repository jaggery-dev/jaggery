package mdns

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	// aliasTTL is short so that when the holder of a shared name goes away
	// without saying goodbye (a laptop lid closing), phones stop using its
	// address within a minute.
	aliasTTL = 30

	// aliasCheck is how often the holder decision is re-read.
	aliasCheck = 5 * time.Second

	// probeWait is how long a starting agent listens after asking who holds
	// the name, before it may claim it (RFC 6762 §8 probes for 750ms).
	probeWait = time.Second

	// cacheFlush marks a record as unique (RFC 6762 §10.2): a client replaces
	// whatever it cached for the name instead of adding to it, which is what
	// moves a phone to the new holder when the name changes hands.
	cacheFlush = 1 << 15
)

// ServeAlias answers for an extra host name, such as jaggery.local, while
// holds reports true, and stays silent otherwise. Several machines can run it
// for the same name; whichever one holds elects is the one that answers.
// Taking the name is announced, and giving it up sends a goodbye, so caches
// follow the holder rather than waiting out the TTL.
//
// holds is told whether another machine has recently claimed the name, so the
// caller can decide who backs off. Backing off is the only remedy that works:
// re-announcing over a rival was measured not to help, because a querying
// client takes whichever answers arrive and the rival keeps answering.
func (c *Conn) ServeAlias(ctx context.Context, name string, addrs []netip.Addr, ipv6 func() []netip.Addr, holds func(contested bool) bool) error {
	name = dns.Fqdn(name)

	var (
		mu        sync.Mutex
		rivalSeen time.Time
		probing   = true
	)
	contested := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !rivalSeen.IsZero() && time.Since(rivalSeen) < 2*aliasTTL*time.Second
	}

	remove := c.addHandler(func(msg *dns.Msg, _ netip.Addr) {
		if msg.Response {
			if rivalClaims(msg, name, addrs) {
				mu.Lock()
				rivalSeen = time.Now()
				mu.Unlock()
			}
			return
		}
		mu.Lock()
		quiet := probing
		mu.Unlock()
		if quiet {
			return // our own probe loops back; answering it would claim the name
		}
		for _, q := range msg.Question {
			if aliasAsked(q, name) && holds(contested()) {
				_ = c.send(aliasResponse(name, addrs, published6(ipv6), aliasTTL))
				return
			}
		}
	})
	defer remove()

	// Probe: ask who holds the name and listen before claiming it, so an
	// agent starting up isolated hears the holder rather than claiming over
	// it until the holder's next announcement (measured: ~14s of split-brain).
	probe := new(dns.Msg)
	probe.SetQuestion(name, dns.TypeA)
	probe.RecursionDesired = false
	_ = c.send(probe)
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(probeWait):
	}
	mu.Lock()
	probing = false
	mu.Unlock()

	ticker := time.NewTicker(aliasCheck)
	defer ticker.Stop()

	holding := false
	var lastAnnounce time.Time
	for {
		now := holds(contested())
		switch {
		case now && (!holding || time.Since(lastAnnounce) >= aliasTTL/2*time.Second):
			if err := c.send(aliasResponse(name, addrs, published6(ipv6), aliasTTL)); err != nil {
				return err
			}
			lastAnnounce = time.Now()
		case !now && holding:
			_ = c.send(aliasResponse(name, addrs, published6(ipv6), 0)) // goodbye
		}
		holding = now

		select {
		case <-ctx.Done():
			if holding {
				_ = c.send(aliasResponse(name, addrs, published6(ipv6), 0))
			}
			return nil
		case <-ticker.C:
		}
	}
}

// rivalClaims reports whether a response is another machine claiming name: an
// A record for it, with a live TTL, for an address that is not ours. Our own
// announcements loop back and are ignored.
//
// Goodbyes (TTL 0) are deliberately not treated as "the name is free": during
// a handover the old holder's goodbye arrives while the new one is claiming,
// and an isolated machine that took the goodbye at its word jumped in (seen
// live). "Contested" instead expires on its own once no claim has been heard.
func rivalClaims(msg *dns.Msg, name string, ours []netip.Addr) bool {
	return len(claimedAddrs(msg, name, ours)) > 0
}

// claimedAddrs are the addresses, other than ours, that a response gives for
// name with a live TTL.
func claimedAddrs(msg *dns.Msg, name string, ours []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, rr := range append(msg.Answer, msg.Extra...) {
		a, ok := rr.(*dns.A)
		if !ok || a.Hdr.Ttl == 0 || !equalName(a.Hdr.Name, name) {
			continue
		}
		addr, ok := netip.AddrFromSlice(a.A)
		if ok && !containsAddr(ours, addr.Unmap()) {
			out = append(out, addr.Unmap())
		}
	}
	return out
}

// WatchClaims calls seen for every address, other than ours, that answers
// for one of names, until ctx ends. It only listens: the caller decides
// which addresses belong to the house.
func (c *Conn) WatchClaims(ctx context.Context, names []string, ours []netip.Addr, seen func(name string, addr netip.Addr)) {
	fqdns := make([]string, len(names))
	for i, n := range names {
		fqdns[i] = dns.Fqdn(n)
	}
	remove := c.addHandler(func(msg *dns.Msg, _ netip.Addr) {
		if !msg.Response {
			return
		}
		for _, name := range fqdns {
			for _, addr := range claimedAddrs(msg, name, ours) {
				seen(name, addr)
			}
		}
	})
	<-ctx.Done()
	remove()
}

// aliasAsked includes AAAA questions: we have no IPv6 answer, but replying
// with the NSEC below tells the asker so, instead of leaving it to time out.
func aliasAsked(q dns.Question, name string) bool {
	switch q.Qtype {
	case dns.TypeA, dns.TypeAAAA, dns.TypeANY:
		return equalName(q.Name, name)
	default:
		return false
	}
}

// aliasResponse carries one A record per IPv4 address. A TTL of 0 is a goodbye.
//
// It also carries an NSEC record asserting the name has only A records
// (RFC 6762 §6.1). Without it, macOS asks for IPv4 and IPv6 together and waits
// out the unanswered IPv6 query: measured at 2-5 seconds per lookup.
func published6(ipv6 func() []netip.Addr) []netip.Addr {
	if ipv6 == nil {
		return nil
	}
	return ipv6()
}

// aliasResponse answers for name with the IPv4 addresses, and the IPv6 ones
// when given. Linux's Avahi waits 5 s for an IPv6 answer and ignores the NSEC
// that tells macOS there is none, so a real AAAA is what makes
// the names fast there; it's only given once a peer has confirmed the port
// answers over IPv6, or apps would hang on it instead.
func aliasResponse(name string, addrs, addrs6 []netip.Addr, ttl uint32) *dns.Msg {
	msg := new(dns.Msg)
	msg.Response = true
	msg.Authoritative = true
	for _, addr := range addrs {
		if !addr.Is4() {
			continue
		}
		msg.Answer = append(msg.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET | cacheFlush, Ttl: ttl},
			A:   net.IP(addr.AsSlice()),
		})
	}
	types := []uint16{dns.TypeA}
	for _, addr := range addrs6 {
		if !addr.Is6() {
			continue
		}
		msg.Answer = append(msg.Answer, &dns.AAAA{
			Hdr:  dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET | cacheFlush, Ttl: ttl},
			AAAA: net.IP(addr.AsSlice()),
		})
		if len(types) == 1 {
			types = append(types, dns.TypeAAAA)
		}
	}
	msg.Extra = append(msg.Extra, &dns.NSEC{
		Hdr:        dns.RR_Header{Name: name, Rrtype: dns.TypeNSEC, Class: dns.ClassINET | cacheFlush, Ttl: ttl},
		NextDomain: name,
		TypeBitMap: types,
	})
	return msg
}
