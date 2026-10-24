package house

import (
	"testing"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

func TestNameHolderIsLowestReachableApprovedID(t *testing.T) {
	snap := Snapshot{
		Self: Machine{MachineID: "71df", Self: true, Reachable: true},
		Peers: []Machine{
			{MachineID: "d47f", Trust: home.Approved, Reachable: true},
			{MachineID: "54da", Trust: home.Approved, Reachable: true},
		},
	}
	if got := snap.NameHolder(); got != "54da" {
		t.Fatalf("holder = %q, want 54da", got)
	}
}

func TestNameHolderSkipsUnreachableAndUnapprovedPeers(t *testing.T) {
	snap := Snapshot{
		Self: Machine{MachineID: "71df", Self: true, Reachable: true},
		Peers: []Machine{
			{MachineID: "0000", Trust: home.Pending, Reachable: true},   // not trusted
			{MachineID: "1111", Trust: home.Approved, Reachable: false}, // asleep
		},
	}
	if got := snap.NameHolder(); got != "71df" {
		t.Fatalf("holder = %q, want self (71df)", got)
	}
}

func TestNameHolderEmptyWithoutSnapshot(t *testing.T) {
	if got := (Snapshot{}).NameHolder(); got != "" {
		t.Fatalf("holder = %q, want empty: an agent with no snapshot yet must not claim the names", got)
	}
}

func TestIsolatedMachineDefersOnlyWhenContested(t *testing.T) {
	cutOff := Snapshot{
		Self:  Machine{MachineID: "d47f", Self: true, Reachable: true},
		Peers: []Machine{{MachineID: "54da", Trust: home.Approved, Reachable: false}},
	}
	if !cutOff.Isolated() {
		t.Fatal("approved peers, none reachable: should be isolated")
	}
	if !cutOff.HoldsNames("d47f", false) {
		t.Error("uncontested: the last machine standing should hold the names")
	}
	if cutOff.HoldsNames("d47f", true) {
		t.Error("contested while isolated: should defer, it may be the one cut off")
	}
}

func TestHealthyHolderKeepsNamesWhenContested(t *testing.T) {
	healthy := Snapshot{
		Self:  Machine{MachineID: "54da", Self: true, Reachable: true},
		Peers: []Machine{{MachineID: "d47f", Trust: home.Approved, Reachable: true}},
	}
	if healthy.Isolated() {
		t.Fatal("reaches its peer: not isolated")
	}
	if !healthy.HoldsNames("54da", true) {
		t.Error("the elected holder with a full view must keep the names against a rival")
	}
	if healthy.HoldsNames("d47f", false) {
		t.Error("only the elected holder answers")
	}
}

func TestAloneMachineIsNotIsolated(t *testing.T) {
	alone := Snapshot{Self: Machine{MachineID: "71df", Self: true, Reachable: true}}
	if alone.Isolated() {
		t.Error("a house of one has nobody to be cut off from")
	}
}

func machine(id string, servesWeb, laptop bool) Machine {
	return Machine{MachineID: id, Name: id, Trust: home.Approved, Reachable: true,
		Inventory: inventory.Report{ServesWeb: servesWeb, Laptop: laptop}}
}

// The C2 done-when: with the Pi off, the names go to the desktop, never the laptop,
// even when the laptop's id is the lowest.
func TestNameHolderNeverPicksALaptopWhenADesktopIsUp(t *testing.T) {
	laptop := machine("0aaa", true, true)
	desktop := machine("71df", true, false)
	for _, self := range []Machine{laptop, desktop} {
		snap := Snapshot{Self: self}
		for _, m := range []Machine{laptop, desktop} {
			if m.MachineID != self.MachineID {
				snap.Peers = append(snap.Peers, m)
			}
		}
		snap.Self.Self, snap.Self.Trust = true, ""
		if got := snap.NameHolder(); got != desktop.MachineID {
			t.Errorf("seen from %s: holder %s, want the desktop", self.Name, got)
		}
	}
}

// A machine that holds port 80 beats one that doesn't: otherwise
// http://jaggery.local would need a port.
func TestNameHolderPrefersPort80(t *testing.T) {
	pi := machine("54da", false, false) // port 80 taken by something else
	desktop := machine("71df", true, false)
	snap := Snapshot{Self: pi, Peers: []Machine{desktop}}
	if got := snap.NameHolder(); got != desktop.MachineID {
		t.Fatalf("holder %s, want the machine serving port 80", got)
	}
}

// A house of laptops still has a holder.
func TestOnlyLaptopsStillElectOne(t *testing.T) {
	a, b := machine("0aaa", true, true), machine("0bbb", true, true)
	if got := (Snapshot{Self: b, Peers: []Machine{a}}).NameHolder(); got != a.MachineID {
		t.Fatalf("holder %s, want the lowest id", got)
	}
}

// R2: while a reachable machine runs a build that only knows the lowest-id
// rule, everyone uses it, so old and new agree on one holder.
func TestMixedVersionsAgreeOnTheOldRule(t *testing.T) {
	pi := machine("54da", false, false) // lost port 80: the new rule would skip it
	pi.Inventory.OS, pi.Inventory.Version = "linux", "0.5.0-beta.20 (abc, built …)"
	desktop := machine("71df", true, false)
	desktop.Inventory.OS, desktop.Inventory.Version = "linux", "0.5.0-beta.20 (abc, built …)"
	laptop := machine("d47f", true, true)
	laptop.Inventory.OS, laptop.Inventory.Version = "darwin", "beta.7 (92364d5, built 2026-10-07 13:59)"

	snap := Snapshot{Self: desktop, Peers: []Machine{pi, laptop}}
	snap.Self.Self = true
	if got := snap.NameHolder(); got != pi.MachineID {
		t.Fatalf("with an old agent up, holder %s, want the lowest id (as the old agent sees it)", got)
	}

	snap.Peers[1].Reachable = false // the old agent leaves
	if got := snap.NameHolder(); got != desktop.MachineID {
		t.Fatalf("all new: holder %s, want the machine serving port 80", got)
	}
}

func TestRanksHolders(t *testing.T) {
	cases := map[string]bool{
		"beta.7 (92364d5, built 2026-10-07 13:59)":  false,
		"beta.8 (aaaaaaa, built 2026-10-07 14:53)":  true,
		"beta.11 (e64a16d, built 2026-10-07 15:33)": true,
		"0.5.0-beta.16 (b0fa1e2, built 2026-10-07)": true,
		"dev build 0.0.0-dev+ed6ec7f":               true,
		"":                                          false,
	}
	for v, want := range cases {
		if got := RanksHolders(v); got != want {
			t.Errorf("RanksHolders(%q) = %v, want %v", v, got, want)
		}
	}
}

// Decided after V2: a desktop without port 80 beats a laptop with it,
// because the laptop's names vanish when it sleeps.
func TestNameHolderPrefersAlwaysOnOverPort80(t *testing.T) {
	pi := machine("54da", false, false)   // no port 80, always on
	laptop := machine("0aaa", true, true) // port 80, laptop, lowest id
	snap := Snapshot{Self: laptop, Peers: []Machine{pi}}
	if got := snap.NameHolder(); got != pi.MachineID {
		t.Fatalf("holder %s, want the always-on machine", got)
	}
}
