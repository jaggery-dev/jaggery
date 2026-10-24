package main

import (
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
)

// claimMemory is how long a claim on a shared name is shown after it was
// last heard.
const claimMemory = 10 * time.Minute

// claimLog remembers every other address heard answering for jaggery.local
// or ai.local. Whether an address belongs to the house is decided
// when the snapshot is written, against the machines approved then.
type claimLog struct {
	mu   sync.Mutex
	seen map[string]*house.NameClaim // by name and address
}

var nameClaims = &claimLog{}

func (l *claimLog) add(name string, addr netip.Addr, now time.Time) {
	name = strings.TrimSuffix(name, ".")
	key := name + " " + addr.String()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]*house.NameClaim{}
	}
	c, ok := l.seen[key]
	if !ok {
		c = &house.NameClaim{Name: name, Addr: addr, FirstSeen: now.UTC()}
		l.seen[key] = c
	}
	c.LastSeen = now.UTC()
}

// foreign is the claims heard in the last claimMemory from addresses that
// aren't an approved machine's. A Jaggery machine that isn't approved here
// is named, since it may be a neighbour's house on the same network.
func (l *claimLog) foreign(peers []home.Peer, now time.Time) []house.NameClaim {
	approved := map[netip.Addr]bool{}
	known := map[netip.Addr]string{}
	for _, p := range peers {
		for _, a := range p.Addrs {
			if p.Trust == home.Approved {
				approved[a] = true
			} else {
				known[a] = p.Name
			}
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []house.NameClaim
	for key, c := range l.seen {
		if now.Sub(c.LastSeen) > claimMemory {
			delete(l.seen, key)
			continue
		}
		if approved[c.Addr] {
			continue
		}
		claim := *c
		claim.Machine = known[c.Addr]
		out = append(out, claim)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Addr.Less(out[j].Addr)
	})
	return out
}

// claimText says what was heard, for jag doctor and the journal.
func claimText(c house.NameClaim) string {
	who := "Something at " + c.Addr.String()
	if c.Machine != "" {
		who = c.Machine + " at " + c.Addr.String() + ", a Jaggery machine not approved here,"
	}
	return who + " also answers for " + c.Name
}

func claimedBefore(prev []house.NameClaim, c house.NameClaim) bool {
	for _, p := range prev {
		if p.Name == c.Name && p.Addr == c.Addr {
			return true
		}
	}
	return false
}
