// Package house holds the assembled view of every machine in the home: this
// one plus each approved peer, with the inventory last collected from it. The
// agent writes a snapshot periodically; `jag map` and the dashboard read it.
//
// The snapshot is written by the single running agent and swapped in by
// rename, so a reader in another process always sees a whole file without
// needing a lock.
package house

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

const fileName = "house.json"

// Machine is one entry in the map: who it is, whether it is reachable, and
// what it last reported about itself.
type Machine struct {
	MachineID string     `json:"machine_id"`
	Name      string     `json:"name"`
	Trust     home.Trust `json:"trust,omitempty"` // empty for self
	// Incompatible: its house protocol doesn't overlap this machine's, so
	// it's shown on the map but given no work.
	Incompatible bool         `json:"incompatible,omitempty"`
	Self         bool         `json:"self,omitempty"`
	Reachable    bool         `json:"reachable"`
	Addrs        []netip.Addr `json:"addrs,omitempty"`
	// Fingerprint is its key's, once proven: what tells two machines with
	// one name apart.
	Fingerprint string           `json:"fingerprint,omitempty"`
	Inventory   inventory.Report `json:"inventory"`
	InventoryAt time.Time        `json:"inventory_at"`
}

type Snapshot struct {
	Self      Machine   `json:"self"`
	Peers     []Machine `json:"peers"`
	UpdatedAt time.Time `json:"updated_at"`
	// Holder is NameHolder at the time of writing, for the dashboard.
	Holder string `json:"name_holder,omitempty"`
	// NameClaims are devices outside the house heard answering for
	// jaggery.local or ai.local: they may get what is sent there.
	NameClaims []NameClaim `json:"name_claims,omitempty"`
}

// NameClaim is an address that isn't a machine in the house answering for
// one of the shared names. Machine is set when it is a Jaggery machine this
// one knows but hasn't approved.
type NameClaim struct {
	Name      string     `json:"name"`
	Addr      netip.Addr `json:"addr"`
	Machine   string     `json:"machine,omitempty"`
	FirstSeen time.Time  `json:"first_seen"`
	LastSeen  time.Time  `json:"last_seen"`
}

// All returns self first, then peers — the order the map is drawn in.
func (s Snapshot) All() []Machine {
	return append([]Machine{s.Self}, s.Peers...)
}

func Save(dir string, snap Snapshot) error {
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding house: %w", err)
	}
	path := filepath.Join(dir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("writing house: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saving house: %w", err)
	}
	return nil
}

var ErrNoSnapshot = errors.New("no house snapshot yet: is the agent running?")

func Load(dir string) (Snapshot, error) {
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, ErrNoSnapshot
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading house: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("parsing house: %w", err)
	}
	return snap, nil
}

// NameHolder picks the machine that answers for the house's shared names
// (jaggery.local, ai.local) among this machine and the approved peers it can
// reach. Any agent can serve both names (the dashboard draws the whole house;
// the router reaches every model), so the choice is about staying reachable:
// prefer a machine that isn't a laptop, which sleeps and leaves,
// then one that holds port 80, so http://jaggery.local needs no port, then
// the lowest machine ID. Every agent ranks the same shared facts, so they agree.
//
// During a rollout some machines still run builds that only know the older
// rule (lowest id). They can't learn the new one, so while any reachable
// machine is that old, everyone uses the old rule and the house still agrees
// on one holder.
func (s Snapshot) NameHolder() string {
	candidates := []Machine{s.Self}
	for _, p := range s.Peers {
		if p.Trust == home.Approved && p.Reachable {
			candidates = append(candidates, p)
		}
	}
	ranked := true
	for _, m := range candidates {
		if m.Inventory.OS != "" && !RanksHolders(m.Inventory.Version) {
			ranked = false
		}
	}
	best := Machine{}
	for _, m := range candidates {
		switch {
		case best.MachineID == "":
			best = m
		case ranked && betterHolder(m, best):
			best = m
		case !ranked && m.MachineID < best.MachineID:
			best = m
		}
	}
	return best.MachineID
}

// RanksHolders reports whether a build (as its inventory's Version says)
// ranks name holders by port 80 and laptops (C2, beta.8 and later, and every
// SemVer build), rather than by lowest id alone.
func RanksHolders(v string) bool {
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "dev build") {
		return true
	}
	if strings.HasPrefix(v, "beta.") {
		n, err := strconv.Atoi(strings.TrimPrefix(strings.Fields(v)[0], "beta."))
		return err == nil && n >= 8
	}
	return true // SemVer builds came after C2
}

// betterHolder ranks a machine that stays on above one that holds port 80:
// names on a laptop vanish when its lid closes, while names on a machine
// without port 80 still work with a port (decided 2026-10-08, after V2).
func betterHolder(a, b Machine) bool {
	if a.Inventory.Laptop != b.Inventory.Laptop {
		return !a.Inventory.Laptop
	}
	if a.Inventory.ServesWeb != b.Inventory.ServesWeb {
		return a.Inventory.ServesWeb
	}
	return a.MachineID < b.MachineID
}

// Isolated reports that this machine has approved peers but reaches none of
// them. Its own election then only ever picks itself, which is right if the
// others are all off and wrong if it is the one cut off; it cannot tell which,
// so when another machine is answering for the shared names it should defer.
func (s Snapshot) Isolated() bool {
	approved := false
	for _, p := range s.Peers {
		if p.Trust != home.Approved {
			continue
		}
		if p.Reachable {
			return false
		}
		approved = true
	}
	return approved
}

// HoldsNames decides whether machineID should answer for the shared names,
// given whether another machine is currently claiming them.
func (s Snapshot) HoldsNames(machineID string, contested bool) bool {
	if s.NameHolder() != machineID {
		return false
	}
	return !(contested && s.Isolated())
}
