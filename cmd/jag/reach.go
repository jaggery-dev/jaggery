package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	goruntime "runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/apps"
	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/netif"
	"github.com/jaggery-dev/jaggery/internal/reach"
	"github.com/jaggery-dev/jaggery/internal/shares"
	"github.com/jaggery-dev/jaggery/internal/update"
)

const (
	reachFirst    = 20 * time.Second // give peers time to be verified first
	reachInterval = 5 * time.Minute
	reachTimeout  = 3 * time.Second
)

// lastInbound is this agent's latest dial-back result, attached to every
// inventory report it hands out.
var lastInbound atomic.Pointer[reach.Report]

// appCache keeps app detection to once a minute; peers ask for inventory far
// more often than apps change.
var appCache = apps.Cache{TTL: time.Minute, Catalog: apps.Catalog, Custom: func() []apps.Custom {
	dir, err := identity.StateDir()
	if err != nil {
		return nil
	}
	list, _ := apps.LoadCustom(dir)
	return list
}, Names: func() []apps.Name {
	dir, err := identity.StateDir()
	if err != nil {
		return nil
	}
	list, _ := apps.LoadNames(dir)
	return list
}}

// ipv6Ready is set once a peer has reached port 80 here over IPv6; only
// then do the shared names carry this machine's IPv6 address.
var ipv6Ready atomic.Bool

// ipv6Seen decides ipv6Ready from the dial-back results: one
// success turns IPv6 on, but it takes ipv6Misses failures in a row to turn
// it off, because single checks over Wi-Fi time out now and then and the
// names would flap. A run with no IPv6 verdict (an older peer) changes
// nothing.
var ipv6Seen ipv6State

const ipv6Misses = 3

type ipv6State struct {
	ready  bool
	misses int
}

func (s *ipv6State) observe(checked, open bool) (ready, changed bool) {
	switch {
	case !checked:
	case open:
		s.misses = 0
		if !s.ready {
			s.ready, changed = true, true
		}
	default:
		s.misses++
		if s.ready && s.misses >= ipv6Misses {
			s.ready, changed = false, true
		}
	}
	return s.ready, changed
}

// ipv6ForNames is what the shared names publish for IPv6: this machine's
// link-local addresses, once they're confirmed reachable.
func ipv6ForNames() []netip.Addr {
	if !ipv6Ready.Load() {
		return nil
	}
	return ownLinkLocal6Addrs()
}

func ownLinkLocal6Addrs() []netip.Addr {
	usable, _, err := netif.Usable()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, iface := range usable {
		out = append(out, iface.LinkLocal6...)
	}
	return out
}

func ownLinkLocal6() []string {
	var out []string
	for _, a := range ownLinkLocal6Addrs() {
		out = append(out, a.String())
	}
	return out
}

// lanZones are the interfaces a link-local IPv6 address is dialled through.
func lanZones() []string {
	usable, _, err := netif.Usable()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range usable {
		out = append(out, iface.Name)
	}
	return out
}

// servesWeb is set once the agent holds port 80.
var servesWeb atomic.Bool

// collectInventory is inventory.Collect plus what only the running agent knows.
func collectInventory() inventory.Report {
	r := inventory.Collect()
	r.Inbound = lastInbound.Load()
	r.Update = lastUpdate.Load()
	r.ServesWeb = servesWeb.Load()
	r.RunningSince = agentStarted
	if dir, err := identity.StateDir(); err == nil {
		_, r.UpdatePending = update.LoadPending(dir)
	}
	addModelFacts(&r, time.Now())
	if dir, err := identity.StateDir(); err == nil {
		r.BadCommits = update.Bad(dir)
		r.WithdrawnCommits = update.Withdrawn(dir)
		r.Shares = shares.Infos(dir, time.Now())
	}
	if addr, ok := lanAddr(); ok {
		r.Apps = appCache.Get(context.Background(), addr, time.Now())
	}
	return r
}

// lanAddr is the IPv4 address the house reaches this machine on.
func lanAddr() (netip.Addr, bool) {
	usable, _, err := netif.Usable()
	if err != nil {
		return netip.Addr{}, false
	}
	for _, iface := range usable {
		for _, a := range iface.Addrs {
			if a.Is4() {
				return a, true
			}
		}
	}
	return netip.Addr{}, false
}

// runtimePorts is where a dial-back looks for a caller's AI runtimes; tests
// point it at fakes.
var runtimePorts = airuntime.DefaultPorts

// checkedRuntimes is every runtime kind a dial-back is asked to look for. All
// of them, not just those found on localhost: one bound only to the LAN
// address is invisible locally and is exactly the exposure to report.
var checkedRuntimes = airuntime.Kinds

// reachHandler dials an approved caller back on the agent ports it names, and
// looks for AI runtimes answering on its address. It lives on the secured
// listener, so only approved machines can ask.
func reachHandler(id *identity.Identity) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req reach.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "no caller address", http.StatusBadRequest)
			return
		}
		ports := reach.Allowed(req.Ports, discovery.AgentPort, discovery.SecurePort, discovery.WebPort)
		report := reach.Report{
			CheckedBy: id.Name,
			CheckedAt: time.Now().UTC(),
			Checks:    reach.Dial(r.Context(), ap.Addr().Unmap(), ports, reachTimeout),
		}
		if len(req.Runtimes) > 0 {
			report.Runtimes = reach.CheckRuntimes(r.Context(), ap.Addr().Unmap(), req.Runtimes, runtimePorts)
		}
		if len(req.IPv6) > 0 {
			// Only port 80 matters over IPv6: it's what the shared names
			// lead to. Agents talk to each other over IPv4.
			report.Checks6 = reach.DialIPv6(r.Context(), req.IPv6, lanZones(), reach.Allowed(ports, discovery.WebPort), reachTimeout)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	}
}

// watchReach asks a reachable approved peer to dial this machine back, soon
// after start and then periodically, and reports newly blocked ports.
func watchReach(ctx context.Context, id *identity.Identity, store *home.Store, ports []int) {
	timer := time.NewTimer(reachFirst)
	defer timer.Stop()
	var lastBlocked, lastExposed string
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(reachInterval)

		report, peer, ok := askAnyPeer(ctx, id, store, ports)
		if !ok {
			continue // nobody to ask yet; a house of one has no inbound to check
		}
		fw := reach.DetectFirewall()
		subnet := reach.SubnetOf(peer)
		for i, c := range report.Checks {
			if !c.Open {
				report.Checks[i].Fix = reach.Fix(fw, c.Port, subnet)
			}
		}
		for i, c := range report.Checks6 {
			if !c.Open {
				report.Checks6[i].Fix = reach.Fix(fw, c.Port, "fe80::/10")
			}
		}
		if ok, changed := ipv6Seen.observe(len(report.Checks6) > 0, report.IPv6Open(discovery.WebPort)); changed {
			ipv6Ready.Store(ok)
			if ok {
				fmt.Printf("IPv6 ok: %s reached port %d over IPv6; the shared names now carry this machine's IPv6 address too\n", report.CheckedBy, discovery.WebPort)
			}
		}
		// Warn only once IPv6 is off: a single miss while it's on is the
		// Wi-Fi, not the firewall (R6).
		if !ipv6Ready.Load() && !report.IPv6Open(discovery.WebPort) && len(report.Checks6) > 0 {
			for _, c := range report.Checks6 {
				if !c.Open {
					fmt.Printf("warning: %s cannot reach port %d here over IPv6, so the shared names stay IPv4-only (slow on Linux); to fix: %s\n", report.CheckedBy, c.Port, c.Fix)
				}
			}
		}
		for i, c := range report.Runtimes {
			if c.Exposed {
				report.Runtimes[i].Fix = reach.RuntimeFix(c.Kind, goruntime.GOOS)
			}
		}
		lastInbound.Store(&report)

		if exposed := fmt.Sprint(report.Exposed()); exposed != lastExposed {
			lastExposed = exposed
			for _, c := range report.Exposed() {
				fmt.Printf("warning: %s reached %s on port %d here: anyone on the network can use and change its models without Jaggery; to fix: %s\n",
					report.CheckedBy, c.Kind, c.Port, c.Fix)
			}
		}

		if blocked := fmt.Sprint(report.Blocked()); blocked != lastBlocked {
			lastBlocked = blocked
			for _, c := range report.Checks {
				if !c.Open {
					fmt.Printf("warning: %s cannot reach port %d here; to fix: %s\n", report.CheckedBy, c.Port, c.Fix)
				}
			}
			if len(report.Blocked()) == 0 {
				fmt.Printf("inbound ok: %s reached ports %v\n", report.CheckedBy, ports)
			}
		}
	}
}

func askAnyPeer(ctx context.Context, id *identity.Identity, store *home.Store, ports []int) (reach.Report, netip.Addr, bool) {
	peers, err := store.List()
	if err != nil {
		return reach.Report{}, netip.Addr{}, false
	}
	for _, p := range peers {
		if p.Trust != home.Approved || !p.Verified() || len(p.Addrs) == 0 {
			continue
		}
		report, err := askDialBack(ctx, id, p, ports)
		if err == nil {
			return report, p.Addrs[0], true
		}
	}
	return reach.Report{}, netip.Addr{}, false
}

func askDialBack(ctx context.Context, id *identity.Identity, peer home.Peer, ports []int) (reach.Report, error) {
	// IPv4 then IPv6 dials, then runtime probes (in parallel, 2 s each).
	client, err := peerHTTPClient(id, ed25519.PublicKey(peer.PublicKey), time.Duration(2*len(ports)+2)*reachTimeout+3*time.Second)
	if err != nil {
		return reach.Report{}, err
	}
	body, _ := json.Marshal(reach.Request{Ports: ports, Runtimes: checkedRuntimes, IPv6: ownLinkLocal6()})
	addr := net.JoinHostPort(peer.Addrs[0].String(), strconv.Itoa(discovery.SecurePort))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+reach.Path, bytes.NewReader(body))
	if err != nil {
		return reach.Report{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return reach.Report{}, err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return reach.Report{}, fmt.Errorf("%s answered %s", peer.Name, resp.Status)
	}
	var report reach.Report
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&report); err != nil {
		return reach.Report{}, err
	}
	return report, nil
}

// printInbound shows whether the house can reach this machine's ports, and
// whether it can reach AI runtimes it shouldn't.
func printInbound(r *reach.Report) {
	if r == nil {
		fmt.Printf("inbound      not checked yet (needs an approved peer)\n")
		return
	}
	for _, c := range r.Exposed() {
		fmt.Printf("exposed      %s on port %d answers the whole network (seen from %s)\n", c.Kind, c.Port, r.CheckedBy)
		fmt.Printf("             anyone on the LAN can use, pull or delete its models without Jaggery\n")
		fmt.Printf("             fix: %s\n", c.Fix)
	}
	var open []string
	for _, c := range r.Checks {
		if c.Open {
			open = append(open, strconv.Itoa(c.Port))
		}
	}
	age := time.Since(r.CheckedAt).Round(time.Second)
	if len(r.Blocked()) == 0 {
		fmt.Printf("inbound      ports %v reachable from %s (%s ago)\n", open, r.CheckedBy, age)
		printInbound6(r)
		return
	}
	for _, c := range r.Checks {
		if !c.Open {
			fmt.Printf("inbound      port %d BLOCKED from %s (%s ago)\n", c.Port, r.CheckedBy, age)
			fmt.Printf("             fix: %s\n", c.Fix)
		}
	}
	printInbound6(r)
}

// printInbound6 is the IPv6 half of the dial-back.
func printInbound6(r *reach.Report) {
	for _, c := range r.Checks6 {
		if !c.Open && c.Port == discovery.WebPort {
			fmt.Printf("inbound      port %d BLOCKED over IPv6 (names stay IPv4-only, slow on Linux)\n", c.Port)
			fmt.Printf("             fix: %s\n", c.Fix)
		}
	}
}
