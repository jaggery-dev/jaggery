package netif

import (
	"net"
	"net/netip"
	"testing"
)

func addrs(t *testing.T, raw ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, 0, len(raw))
	for _, s := range raw {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestClassify(t *testing.T) {
	const up = net.FlagUp

	tests := []struct {
		name      string
		candidate Candidate
		wantAddrs int
		wantSkip  string
	}{
		{
			// The real Wi-Fi interface on the dev desktop.
			name:      "ordinary wifi interface is announceable",
			candidate: Candidate{Name: "wlp3s0", Flags: up, Addrs: nil},
			wantSkip:  "no routable address",
		},
		{
			name:      "wifi with a private address",
			candidate: Candidate{Name: "wlp3s0", Flags: up},
			wantSkip:  "no routable address",
		},
		{
			name:      "loopback is skipped",
			candidate: Candidate{Name: "lo", Flags: up | net.FlagLoopback},
			wantSkip:  "loopback",
		},
		{
			name:      "down interface is skipped",
			candidate: Candidate{Name: "eth0", Flags: 0},
			wantSkip:  "interface is down",
		},
		{
			name:      "point-to-point link is skipped",
			candidate: Candidate{Name: "eth0", Flags: up | net.FlagPointToPoint},
			wantSkip:  "point-to-point link, treated as a tunnel",
		},
		{
			name:      "docker bridge is skipped",
			candidate: Candidate{Name: "docker0", Flags: up},
			wantSkip:  "overlay or virtual interface",
		},
		{
			name:      "macos vpn tunnel is skipped",
			candidate: Candidate{Name: "utun3", Flags: up},
			wantSkip:  "overlay or virtual interface",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, skip := Classify(tt.candidate)
			if skip != tt.wantSkip {
				t.Errorf("skip reason = %q, want %q", skip, tt.wantSkip)
			}
			if len(got) != tt.wantAddrs {
				t.Errorf("announceable addrs = %d, want %d", len(got), tt.wantAddrs)
			}
		})
	}
}

func TestWifiWithRoutableAddressIsAnnounceable(t *testing.T) {
	c := Candidate{
		Name:  "wlp3s0",
		Flags: net.FlagUp,
		Addrs: addrs(t, "192.0.2.65", "fe80::1"),
	}

	got, skip := Classify(c)
	if skip != "" {
		t.Fatalf("unexpected skip: %s", skip)
	}
	if len(got) != 1 || got[0].String() != "192.0.2.65" {
		t.Errorf("announceable = %v, want only the routable LAN address", got)
	}
}

// Regression test for the spike finding: with Tailscale up, avahi advertised
// the machine's tailnet address, which no off-tailnet device can reach.
func TestTailscaleInterfaceIsNeverAnnounced(t *testing.T) {
	c := Candidate{
		Name:  "tailscale0",
		Flags: net.FlagUp,
		Addrs: addrs(t, "100.64.0.10", "fd7a:115c:a1e0::10"),
	}

	if got, skip := Classify(c); skip == "" {
		t.Errorf("tailscale0 was announceable with %v, want it skipped", got)
	}
}

// A tunnel can outlive its daemon and reappear under a different name, so the
// addresses themselves must be rejected too.
func TestTailscaleAddressesAreRejectedUnderAnyName(t *testing.T) {
	for _, addr := range []string{"100.64.0.10", "fd7a:115c:a1e0::10"} {
		c := Candidate{Name: "eth9", Flags: net.FlagUp, Addrs: addrs(t, addr)}
		if _, skip := Classify(c); skip != "no routable address" {
			t.Errorf("addr %s under an innocuous name: skip = %q, want it rejected", addr, skip)
		}
	}
}

// After `tailscale down` the interface lingered with only a link-local address;
// announcing that would hand clients an unusable address.
func TestLinkLocalOnlyInterfaceIsNotAnnounceable(t *testing.T) {
	c := Candidate{Name: "eth0", Flags: net.FlagUp, Addrs: addrs(t, "fe80::c66d:3ec0:b8a:1060")}

	if _, skip := Classify(c); skip != "no routable address" {
		t.Errorf("skip = %q, want %q", skip, "no routable address")
	}
}

func TestUsableDescribesWhatItSkipped(t *testing.T) {
	usable, skipped, err := Usable()
	if err != nil {
		t.Fatalf("Usable: %v", err)
	}

	// Every machine has loopback, so there is always something to explain.
	if len(skipped) == 0 {
		t.Error("want at least one skipped interface with a reason")
	}
	for name, reason := range skipped {
		if reason == "" {
			t.Errorf("interface %s was skipped without a reason", name)
		}
	}
	for _, iface := range usable {
		if len(iface.Addrs) == 0 {
			t.Errorf("usable interface %s has no addresses", iface.Name)
		}
	}
}
