// Package netif decides which network interfaces Jaggery may announce itself on.
package netif

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Overlay networks must never be announced over mDNS. A machine reachable at a
// Tailscale address is unreachable to every device not on the tailnet (phones,
// consoles, guests), and Tailscale may relay that traffic over the internet,
// which contradicts Jaggery's promise to stay on the local network.
var overlayPrefixes = []string{
	"tailscale", "utun", "wg", "tun", "tap", "zt", "nebula", "tinc", "ppp",
	"docker", "br-", "veth", "virbr", "vmnet", "cni", "flannel", "cali",
}

// Tailscale hands out addresses from the CGNAT range and this ULA prefix, so
// they are excluded by address as well as by interface name: a tunnel can
// outlive the daemon that created it and keep answering under another name.
var (
	tailscaleV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

type Interface struct {
	Name  string
	Index int
	Addrs []netip.Addr
	// LinkLocal6 are the interface's IPv6 link-local addresses (fe80::/10).
	// Not announced as the machine's addresses, but published for the
	// shared names once a peer has confirmed they're reachable.
	LinkLocal6 []netip.Addr
}

// Candidate is an interface as observed from the OS, split out from
// enumeration so the policy can be tested without touching real hardware.
type Candidate struct {
	Name  string
	Flags net.Flags
	Addrs []netip.Addr
}

// Classify reports whether Jaggery may announce on an interface, and why not
// when it may not. The reason is shown to users: a silent empty map is the
// failure mode that makes people blame Jaggery for their network.
func Classify(c Candidate) (announceable []netip.Addr, skip string) {
	switch {
	case c.Flags&net.FlagUp == 0:
		return nil, "interface is down"
	case c.Flags&net.FlagLoopback != 0:
		return nil, "loopback"
	case c.Flags&net.FlagPointToPoint != 0:
		return nil, "point-to-point link, treated as a tunnel"
	case isOverlayName(c.Name):
		return nil, "overlay or virtual interface"
	}

	for _, addr := range c.Addrs {
		if UsableAddr(addr) {
			announceable = append(announceable, addr)
		}
	}
	if len(announceable) == 0 {
		return nil, "no routable address"
	}
	return announceable, ""
}

// Usable returns the interfaces Jaggery may announce on, plus the interfaces it
// rejected and why, so callers can explain themselves.
func Usable() (usable []Interface, skipped map[string]string, err error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("listing network interfaces: %w", err)
	}

	skipped = make(map[string]string)
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			skipped[iface.Name] = fmt.Sprintf("could not read addresses: %v", err)
			continue
		}

		candidate := Candidate{Name: iface.Name, Flags: iface.Flags}
		for _, a := range addrs {
			prefix, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if addr, ok := netip.AddrFromSlice(prefix.IP); ok {
				candidate.Addrs = append(candidate.Addrs, addr.Unmap())
			}
		}

		announceable, skip := Classify(candidate)
		if skip != "" {
			skipped[iface.Name] = skip
			continue
		}
		var ll6 []netip.Addr
		for _, a := range candidate.Addrs {
			if a.Is6() && a.IsLinkLocalUnicast() {
				ll6 = append(ll6, a)
			}
		}
		usable = append(usable, Interface{Name: iface.Name, Index: iface.Index, Addrs: announceable, LinkLocal6: ll6})
	}
	return usable, skipped, nil
}

func isOverlayName(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range overlayPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// UsableAddr reports whether an address is worth talking to on a home LAN.
// It applies to peer addresses learned over mDNS as well as our own: a peer
// that advertises a link-local or tunnel address is no more reachable than we
// would be advertising one.
func UsableAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.IsLoopback() || addr.IsUnspecified() ||
		addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return false
	}
	if tailscaleV4.Contains(addr) || tailscaleV6.Contains(addr) {
		return false
	}
	return true
}
