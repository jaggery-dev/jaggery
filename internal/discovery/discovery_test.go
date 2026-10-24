package discovery

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/mdns"
)

func TestTXTRecordsCarryIdentity(t *testing.T) {
	t.Setenv("JAGGERY_STATE_DIR", t.TempDir())
	id, _, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	want := map[string]bool{
		"jag=1":           true,
		"id=" + id.ID:     true,
		"name=" + id.Name: true,
	}
	got := TXTRecords(id)
	for _, record := range got {
		delete(want, record)
	}
	if len(want) != 0 {
		t.Errorf("TXT records %v are missing %v", got, want)
	}
}

func TestPeerFrom(t *testing.T) {
	tests := []struct {
		name     string
		entry    mdns.Entry
		wantOK   bool
		wantID   string
		wantName string
	}{
		{
			name: "a jaggery agent",
			entry: mdns.Entry{
				Instance: "nas",
				Host:     "nas.local.",
				Port:     7373,
				Addrs:    []netip.Addr{netip.MustParseAddr("192.0.2.70")},
				Text:     []string{"jag=1", "id=abc123def456", "name=nas"},
			},
			wantOK: true, wantID: "abc123def456", wantName: "nas",
		},
		{
			name: "falls back to the hostname when no name is advertised",
			entry: mdns.Entry{
				Host: "gamingpc.local.",
				Text: []string{"jag=1", "id=feed0000beef"},
			},
			wantOK: true, wantID: "feed0000beef", wantName: "gamingpc",
		},
		{
			name: "an entry with no machine id is not a usable peer",
			entry: mdns.Entry{
				Host: "mystery.local.",
				Text: []string{"jag=1", "name=mystery"},
			},
			wantOK: false,
		},
		{
			name: "a future protocol version is ignored",
			entry: mdns.Entry{
				Host: "newer.local.",
				Text: []string{"jag=99", "id=abc123def456"},
			},
			wantOK: false,
		},
		{
			name: "some other service on the network",
			entry: mdns.Entry{
				Host: "printer.local.",
				Text: []string{"ty=HP LaserJet"},
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peer, ok := PeerFrom(tt.entry)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if peer.MachineID != tt.wantID {
				t.Errorf("machine id = %q, want %q", peer.MachineID, tt.wantID)
			}
			if peer.Name != tt.wantName {
				t.Errorf("name = %q, want %q", peer.Name, tt.wantName)
			}
		})
	}
}

// A peer advertising a link-local address is no more reachable than we would
// be advertising one, so the same address policy applies in both directions.
func TestPeerFromDropsUnreachableAddresses(t *testing.T) {
	entry := mdns.Entry{
		Host: "nas.local.",
		Port: 7373,
		Addrs: []netip.Addr{
			netip.MustParseAddr("192.0.2.70"),
			netip.MustParseAddr("fe80::1"),
			netip.MustParseAddr("100.64.0.10"),
		},
		Text: []string{"jag=1", "id=abc123def456"},
	}

	peer, ok := PeerFrom(entry)
	if !ok {
		t.Fatal("entry should be a usable peer")
	}
	if len(peer.Addrs) != 1 || peer.Addrs[0].String() != "192.0.2.70" {
		t.Errorf("addrs = %v, want only the routable LAN address", peer.Addrs)
	}
}

func TestPeerStringRendersEachEndpoint(t *testing.T) {
	peer := Peer{
		Name:      "nas",
		MachineID: "abc123def456",
		Port:      7373,
		Addrs:     []netip.Addr{netip.MustParseAddr("192.0.2.70"), netip.MustParseAddr("fd00::1")},
	}

	got := peer.String()
	for _, want := range []string{"192.0.2.70:7373", "[fd00::1]:7373"} {
		if !strings.Contains(got, want) {
			t.Errorf("Peer.String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestPeerStringWithNoAddressesIsHonest(t *testing.T) {
	peer := Peer{Name: "ghost", MachineID: "abc123def456", Port: 7373}

	if got := peer.String(); !strings.Contains(got, "no reachable address") {
		t.Errorf("Peer.String() = %q, want it to say there is no reachable address", got)
	}
}

func TestOpenRefusesWithoutInterfaces(t *testing.T) {
	if _, err := Open(nil); err == nil {
		t.Error("opening with no interfaces should fail loudly, not silently succeed")
	}
}
