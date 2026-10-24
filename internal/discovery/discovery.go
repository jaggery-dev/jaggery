// Package discovery announces this machine over mDNS and finds its peers.
//
// mDNS is only used to bootstrap: it answers "which machines are here", and
// gossip membership takes over liveness once peers are known. That is why
// queries are one-shot and repeated rather than a long-lived subscription.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/mdns"
	"github.com/jaggery-dev/jaggery/internal/netif"
)

const (
	Service = "_jaggery._tcp"

	// AgentPort is the plaintext bootstrap surface: health and the pairing
	// proof exchange. It is announced in the SRV record, so something must
	// actually listen there.
	AgentPort = 7373

	// WebPort is where the shared names are meant to be opened, so that
	// http://jaggery.local and http://ai.local work without a port. It serves
	// the same surface as AgentPort; AgentPort stays the one peers rely on,
	// since port 80 may be taken or not bindable without privileges.
	WebPort = 80

	// SecurePort is the mutual-TLS surface, reachable only by approved peers.
	// Everything peer-to-peer beyond first-contact pairing goes here.
	SecurePort = 7374

	protocolVersion = "1"
)

type Peer struct {
	MachineID string
	Name      string
	Host      string
	Port      int
	Addrs     []netip.Addr
}

func (p Peer) String() string {
	endpoints := make([]string, 0, len(p.Addrs))
	for _, a := range p.Addrs {
		endpoints = append(endpoints, netip.AddrPortFrom(a, uint16(p.Port)).String())
	}
	if len(endpoints) == 0 {
		return fmt.Sprintf("%s (%s) with no reachable address", p.Name, p.MachineID)
	}
	return fmt.Sprintf("%s (%s) at %s", p.Name, p.MachineID, strings.Join(endpoints, ", "))
}

// Session owns the mDNS socket. Announcing and querying share it because only
// one socket can usefully hold port 5353.
type Session struct {
	conn   *mdns.Conn
	ctx    context.Context
	cancel context.CancelFunc
	errs   chan error
}

// Open binds the mDNS socket and starts reading.
func Open(ifaces []netif.Interface) (*Session, error) {
	if len(ifaces) == 0 {
		return nil, errors.New("no usable network interface: other machines cannot reach this one")
	}

	netIfaces := make([]net.Interface, 0, len(ifaces))
	for _, iface := range ifaces {
		resolved, err := net.InterfaceByName(iface.Name)
		if err != nil {
			return nil, fmt.Errorf("resolving interface %s: %w", iface.Name, err)
		}
		netIfaces = append(netIfaces, *resolved)
	}

	conn, err := mdns.Open(netIfaces)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{conn: conn, ctx: ctx, cancel: cancel, errs: make(chan error, 8)}
	go func() { s.errs <- conn.Run(ctx) }()
	return s, nil
}

// Announce publishes this machine until the session is closed.
func (s *Session) Announce(id *identity.Identity, port int, ifaces []netif.Interface) {
	addrs := make([]netip.Addr, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs = append(addrs, iface.Addrs...)
	}

	svc := mdns.Service{
		Instance: id.Name,
		Type:     Service,
		Host:     id.Name + ".local.",
		Port:     port,
		Text:     TXTRecords(id),
		Addrs:    addrs,
	}
	go func() { s.errs <- s.conn.Serve(s.ctx, svc) }()
}

// SharedNames are the house-wide names one elected agent answers for. Both are
// served by the same listener: the dashboard and the AI endpoints use
// different paths, so the name only tells the person what they are opening.
var SharedNames = []string{"jaggery.local.", "ai.local."}

// AnnounceAlias answers for name with this machine's addresses whenever holds
// reports true, until the session is closed. holds is told whether another
// machine is currently claiming the name.
// ipv6 gives the IPv6 addresses to publish too, or none.
func (s *Session) AnnounceAlias(name string, ifaces []netif.Interface, ipv6 func() []netip.Addr, holds func(contested bool) bool) {
	addrs := make([]netip.Addr, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs = append(addrs, iface.Addrs...)
	}
	go func() { s.errs <- s.conn.ServeAlias(s.ctx, name, addrs, ipv6, holds) }()
}

// WatchSharedNames calls seen for each address, other than this machine's,
// that answers for one of SharedNames, until the session is closed.
func (s *Session) WatchSharedNames(ifaces []netif.Interface, seen func(name string, addr netip.Addr)) {
	var addrs []netip.Addr
	for _, iface := range ifaces {
		addrs = append(addrs, iface.Addrs...)
	}
	go s.conn.WatchClaims(s.ctx, SharedNames, addrs, seen)
}

// AnnounceAliasUntil is AnnounceAlias until ctx ends, for names that come
// and go, such as the house's app names (track W). Ending sends a goodbye if
// this machine was answering.
func (s *Session) AnnounceAliasUntil(ctx context.Context, name string, ifaces []netif.Interface, ipv6 func() []netip.Addr, holds func(contested bool) bool) {
	addrs := make([]netip.Addr, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs = append(addrs, iface.Addrs...)
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		select {
		case <-s.ctx.Done():
		case <-ctx.Done():
		}
	}()
	go func() { _ = s.conn.ServeAlias(ctx, name, addrs, ipv6, holds) }()
}

// Find queries for peers, skipping this machine.
func (s *Session) Find(ctx context.Context, selfMachineID string, timeout time.Duration) ([]Peer, error) {
	entries, err := s.conn.Query(ctx, Service, timeout)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]Peer, len(entries))
	for _, entry := range entries {
		peer, ok := PeerFrom(entry)
		if !ok || peer.MachineID == selfMachineID {
			continue
		}
		if _, dup := seen[peer.MachineID]; !dup {
			seen[peer.MachineID] = peer
		}
	}

	peers := make([]Peer, 0, len(seen))
	for _, p := range seen {
		peers = append(peers, p)
	}
	return peers, nil
}

func (s *Session) Close() error {
	s.cancel()
	return s.conn.Close()
}

func TXTRecords(id *identity.Identity) []string {
	return []string{
		"jag=" + protocolVersion,
		"id=" + id.ID,
		"name=" + id.Name,
	}
}

// PeerFrom converts an mDNS entry into a peer, reporting false for anything
// that is not a Jaggery agent we understand.
func PeerFrom(entry mdns.Entry) (Peer, bool) {
	peer := Peer{Host: entry.Host, Port: entry.Port}
	for _, field := range entry.Text {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case "id":
			peer.MachineID = value
		case "name":
			peer.Name = value
		case "jag":
			if value != protocolVersion {
				return Peer{}, false
			}
		}
	}

	// Without a machine ID there is no identity to pair with or route to.
	if peer.MachineID == "" {
		return Peer{}, false
	}

	for _, addr := range entry.Addrs {
		if netif.UsableAddr(addr) {
			peer.Addrs = append(peer.Addrs, addr)
		}
	}
	if peer.Name == "" {
		peer.Name = strings.TrimSuffix(strings.TrimSuffix(entry.Host, "."), ".local")
	}
	return peer, true
}

// LookupAddr names the device at addr from its own mDNS answer, e.g.
// "Alex-iPhone.local".
func (s *Session) LookupAddr(ctx context.Context, addr netip.Addr, timeout time.Duration) (string, error) {
	return s.conn.LookupAddr(ctx, addr, timeout)
}
