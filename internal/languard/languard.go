// Package languard keeps the agent's LAN port for the home network and the
// apps in it, and out of reach of web pages on the internet.
//
// Jaggery trusts the home network and the people on it, so the page,
// ai.local/v1 and ai.local/mcp need no sign-in. That trust must not leak to
// two neighbours that aren't in the house:
//
//   - The internet. The agent listens on every interface, so a machine with a
//     public address (a global IPv6 address with no firewall in front, a
//     cloud box) would answer the world. Requests must come from a private,
//     link-local or on-link address, or one the owner allowed.
//   - Web pages, through the browsers in the house. A page can send requests
//     to http://ai.local, and with DNS rebinding (its own domain pointed at a
//     LAN address) the browser treats them as same-origin and lets it read
//     the answers. Requests must name the agent by a local name or an IP
//     address in their Host header, and a browser's cross-origin requests
//     that change something are refused.
//
// Apps that aren't browsers (curl, SDKs, LibreChat's server, agents) send no
// Origin or Sec-Fetch-Site header and are unaffected.
//
// Requests to the house's app names (chat.local), which Jaggery forwards to
// the app, pass the same source and Host checks but nothing else: the app's
// own sign-in and headers apply, as they do at its machine's address.
package languard

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

// Env vars for what the defaults don't cover: a comma-separated list of
// prefixes (JAGGERY_ALLOW_FROM=198.51.100.0/24) and of host names
// (JAGGERY_ALLOW_HOSTS=jag.example.org).
const (
	EnvAllowFrom  = "JAGGERY_ALLOW_FROM"
	EnvAllowHosts = "JAGGERY_ALLOW_HOSTS"
)

// localSuffixes are names that only resolve inside a network, never through
// public DNS, so a web page can't rebind them. The router's own DNS often
// answers for .lan, .home or .internal.
var localSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".localhost"}

// cgnat is 100.64.0.0/10: carrier-grade NAT and overlay networks such as
// Tailscale.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Guard is the middleware. The zero value is not usable; call New.
type Guard struct {
	allowFrom  []netip.Prefix
	allowHosts map[string]bool
	// peerAddr reports whether addr belongs to an approved machine, which
	// may sit beyond the LAN (jag join --at). Optional.
	peerAddr func(netip.Addr) bool
	// onLink lists the prefixes of this machine's interfaces, so a global
	// IPv6 neighbour on the same home network is allowed.
	onLink func() []netip.Prefix
	cross  *http.CrossOriginProtection
	logf   func(format string, args ...any)
	// forward returns the handler for a request to one of the house's app
	// names, or nil. Optional.
	forward func(*http.Request) http.Handler

	mu     sync.Mutex
	logged map[string]time.Time
}

// Options configure New.
type Options struct {
	AllowFrom  []netip.Prefix
	AllowHosts []string
	PeerAddr   func(netip.Addr) bool
	// OnLink overrides the interface prefixes (tests).
	OnLink func() []netip.Prefix
	// Logf, if set, is told about refused requests, once per source and
	// reason every ten minutes.
	Logf func(format string, args ...any)
	// Forward, if set, returns the handler for a request to an app name
	// (chat.local), or nil for anything else. Such requests still pass the
	// source and Host checks, but get no cross-origin check and no added
	// headers: the app has its own (tech-specs/app-names.md, decision 2).
	Forward func(*http.Request) http.Handler
}

// New builds a Guard from opts plus the JAGGERY_ALLOW_* environment.
func New(opts Options) (*Guard, error) {
	g := &Guard{
		allowFrom:  opts.AllowFrom,
		allowHosts: map[string]bool{},
		peerAddr:   opts.PeerAddr,
		onLink:     opts.OnLink,
		cross:      http.NewCrossOriginProtection(),
		logf:       opts.Logf,
		forward:    opts.Forward,
		logged:     map[string]time.Time{},
	}
	for _, s := range splitList(os.Getenv(EnvAllowFrom)) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("%s: %q is not an address or prefix", EnvAllowFrom, s)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		g.allowFrom = append(g.allowFrom, p.Masked())
	}
	for _, h := range append(opts.AllowHosts, splitList(os.Getenv(EnvAllowHosts))...) {
		g.allowHosts[strings.ToLower(strings.TrimSuffix(h, "."))] = true
	}
	if g.onLink == nil {
		g.onLink = cachedInterfacePrefixes()
	}
	return g, nil
}

// Wrap returns h behind the guard.
func (g *Guard) Wrap(h http.Handler) http.Handler {
	safe := g.cross.Handler(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		if reason := g.refuse(r); reason != "" {
			setHeaders(hdr)
			g.note(r, reason)
			http.Error(w, "refused by Jaggery: "+reason, http.StatusForbidden)
			return
		}
		if g.forward != nil {
			if f := g.forward(r); f != nil {
				f.ServeHTTP(w, r)
				return
			}
		}
		setHeaders(hdr)
		safe.ServeHTTP(w, r)
	})
}

// setHeaders is what Jaggery's own pages and answers carry.
func setHeaders(hdr http.Header) {
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "no-referrer")
}

// refuse says why r may not be served, or "" if it may.
func (g *Guard) refuse(r *http.Request) string {
	if !g.SourceAllowed(remoteAddr(r)) {
		return "this agent answers only its home network (set " + EnvAllowFrom + " to add a network)"
	}
	if !g.HostAllowed(r.Host) {
		return fmt.Sprintf("%q isn't a local name; open this agent as jaggery.local, ai.local, its machine name or its IP address (or set %s)", hostOnly(r.Host), EnvAllowHosts)
	}
	return ""
}

// SourceAllowed reports whether a request from addr may be served.
func (g *Guard) SourceAllowed(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || cgnat.Contains(addr) {
		return true
	}
	for _, p := range g.allowFrom {
		if p.Contains(addr) {
			return true
		}
	}
	for _, p := range g.onLink() {
		if p.Contains(addr) {
			return true
		}
	}
	return g.peerAddr != nil && g.peerAddr(addr)
}

// HostAllowed reports whether a Host header names this agent in a way a web
// page on the internet can't arrange: an IP address, a single-label name, a
// local-only suffix, or a name the owner allowed.
func (g *Guard) HostAllowed(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(hostOnly(host), "."))
	if h == "" {
		// HTTP/1.0 without Host: no browser sends that.
		return true
	}
	if _, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil {
		return true
	}
	if h == "localhost" || !strings.Contains(h, ".") || g.allowHosts[h] {
		return true
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

func (g *Guard) note(r *http.Request, reason string) {
	if g.logf == nil {
		return
	}
	src := remoteAddr(r).String()
	key := src + "|" + reason
	g.mu.Lock()
	last, seen := g.logged[key]
	fresh := !seen || time.Since(last) > 10*time.Minute
	if fresh {
		g.logged[key] = time.Now()
	}
	g.mu.Unlock()
	if fresh {
		g.logf("languard: refused %s %s from %s (Host %q): %s", r.Method, r.URL.Path, src, r.Host, reason)
	}
}

func remoteAddr(r *http.Request) netip.Addr {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap()
	}
	a, _ := netip.ParseAddr(r.RemoteAddr)
	return a
}

// hostOnly strips the port from a Host header, keeping IPv6 brackets off.
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// cachedInterfacePrefixes reads this machine's interface prefixes, at most
// every 30 seconds, so a new address (DHCP, a new IPv6 prefix) counts soon.
func cachedInterfacePrefixes() func() []netip.Prefix {
	var (
		mu   sync.Mutex
		at   time.Time
		list []netip.Prefix
	)
	return func() []netip.Prefix {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < 30*time.Second {
			return list
		}
		at, list = time.Now(), nil
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return list
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(n.IP)
			if !ok {
				continue
			}
			bits, _ := n.Mask.Size()
			// A /32 or /128 (point-to-point, some VPNs) says nothing about
			// neighbours; a default-route-sized prefix would allow everyone.
			if bits == 0 || bits == ip.Unmap().BitLen() {
				continue
			}
			list = append(list, netip.PrefixFrom(ip.Unmap(), bits).Masked())
		}
		return list
	}
}
