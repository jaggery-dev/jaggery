// Package router decides which machine should serve a model request, and
// merges the models available across the house into one list. The decision is
// a pure function of a house snapshot and the speeds observed so far, so the
// policy is testable without a network.
//
// The policy comes from an early spike (tech-specs/decisions.md, "Routing"): free
// memory is measured to be the wrong ranking signal — it picked a node ~5x
// slower — so it only filters, and observed tokens/sec ranks:
//
//  1. a machine with the model already loaded wins, then one loading it for
//     another request (a second load elsewhere would only compete);
//  2. otherwise, machines the model fits on (free GPU memory, or RAM where
//     there is no GPU figure); if it fits nowhere, any machine that has it,
//     since slow beats a 404;
//  3. among those, a machine not yet measured is tried first (so it gets a
//     number), then the fastest measured; ties go to this machine (no network
//     hop), then a stable order.
package router

import (
	"sort"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/house"
)

// Target is the machine chosen to serve a request.
type Target struct {
	MachineID string
	Name      string
	Self      bool
	Loaded    bool // the model was already loaded there
}

// Speeds reports the generation speed observed for a model on a machine.
type Speeds interface {
	TokensPerSec(machineID, model string) (float64, bool)
}

type candidate struct {
	m        house.Machine
	loaded   bool
	loading  bool
	fits     bool
	tps      float64
	measured bool
}

// now is the clock pauses are judged by; tests replace it.
var now = time.Now

// usable is a machine requests can go to: this one or a reachable,
// compatible peer.
func usable(m house.Machine) bool {
	return m.Self || (m.Reachable && !m.Incompatible)
}

// Pick chooses where to serve model. A machine is eligible if it is this one or
// a reachable peer, it hasn't paused AI requests, and one of its runtimes
// offers the model. speeds may be nil.
func Pick(machines []house.Machine, model string, speeds Speeds) (Target, bool) {
	var all []candidate
	t := now()
	for _, m := range machines {
		if !usable(m) || m.Inventory.AIPaused.Active(t) {
			continue
		}
		loaded, size, has := machineHasModel(m, model)
		if !has {
			continue
		}
		c := candidate{m: m, loaded: loaded, loading: machineLoading(m, model), fits: fits(m, size)}
		if speeds != nil {
			c.tps, c.measured = speeds.TokensPerSec(m.MachineID, model)
		}
		all = append(all, c)
	}
	if len(all) == 0 {
		return Target{}, false
	}

	pool := filter(all, func(c candidate) bool { return c.loaded })
	if len(pool) == 0 {
		pool = filter(all, func(c candidate) bool { return c.loading })
	}
	if len(pool) == 0 {
		pool = filter(all, func(c candidate) bool { return c.fits })
	}
	if len(pool) == 0 {
		pool = all
	}

	sort.SliceStable(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		if a.measured != b.measured {
			return !a.measured // try an unmeasured machine once
		}
		if a.measured && a.tps != b.tps {
			return a.tps > b.tps
		}
		if a.m.Self != b.m.Self {
			return a.m.Self // no network hop
		}
		return a.m.Name < b.m.Name
	})

	best := pool[0]
	return Target{
		MachineID: best.m.MachineID,
		Name:      best.m.Name,
		Self:      best.m.Self,
		Loaded:    best.loaded,
	}, true
}

// PausedWith names the machines Pick passed over for model because AI
// requests are paused there, in order.
func PausedWith(machines []house.Machine, model string) []string {
	var out []string
	t := now()
	for _, m := range machines {
		if !usable(m) || !m.Inventory.AIPaused.Active(t) {
			continue
		}
		if _, _, has := machineHasModel(m, model); has {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out
}

func filter(cs []candidate, keep func(candidate) bool) []candidate {
	var out []candidate
	for _, c := range cs {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// fits reports whether a model of size bytes can load on m at full speed: in
// free GPU memory where the machine reports it, otherwise in RAM. An unknown
// size fits, so a runtime that doesn't report sizes is never filtered out.
func fits(m house.Machine, size uint64) bool {
	if size == 0 {
		return true
	}
	gpuKnown := false
	for _, g := range m.Inventory.GPUs {
		if g.FreeBytes == nil {
			continue
		}
		gpuKnown = true
		if *g.FreeBytes >= size {
			return true
		}
	}
	if gpuKnown {
		return false
	}
	return m.Inventory.MemoryBytes == 0 || size <= m.Inventory.MemoryBytes
}

// machineHasModel reports whether any of a machine's runtimes offers the model,
// whether it is loaded there, and its size if known.
func machineHasModel(m house.Machine, model string) (loaded bool, size uint64, has bool) {
	for _, rt := range m.Inventory.Runtimes {
		for _, mo := range rt.Models {
			if modelMatches(mo.Name, model) {
				has = true
				if mo.Loaded {
					loaded = true
				}
				if mo.SizeBytes > size {
					size = mo.SizeBytes
				}
			}
		}
	}
	return loaded, size, has
}

// machineLoading reports whether a machine is loading the model for a
// request now.
func machineLoading(m house.Machine, model string) bool {
	for _, rt := range m.Inventory.Runtimes {
		for _, mo := range rt.Models {
			if modelMatches(mo.Name, model) && mo.LoadingSince != nil {
				return true
			}
		}
	}
	return false
}

// modelMatches accepts an exact name or the name without its ":latest" tag, so
// "llama3.2" matches "llama3.2:latest".
func modelMatches(have, want string) bool {
	if have == want {
		return true
	}
	return strings.TrimSuffix(have, ":latest") == strings.TrimSuffix(want, ":latest")
}

// MergedModels lists every distinct model offered anywhere in the house, in
// OpenAI /v1/models order (sorted by id), with the machines that can serve it.
type MergedModel struct {
	ID       string   `json:"id"`
	Object   string   `json:"object"`
	OwnedBy  string   `json:"owned_by"`
	Machines []string `json:"-"`
}

func MergedModels(machines []house.Machine) []MergedModel {
	byID := map[string][]string{}
	for _, m := range machines {
		if !m.Self && (!m.Reachable || m.Incompatible) {
			continue
		}
		seen := map[string]bool{}
		for _, rt := range m.Inventory.Runtimes {
			for _, mo := range rt.Models {
				if !seen[mo.Name] {
					seen[mo.Name] = true
					byID[mo.Name] = append(byID[mo.Name], m.Name)
				}
			}
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]MergedModel, 0, len(ids))
	for _, id := range ids {
		owned := "jaggery"
		if len(byID[id]) == 1 {
			owned = byID[id][0]
		}
		out = append(out, MergedModel{ID: id, Object: "model", OwnedBy: owned, Machines: byID[id]})
	}
	return out
}

// LocalRuntimeFor returns the endpoint of a local runtime that offers the
// model, for the agent that will actually serve it. Empty if none.
func LocalRuntimeFor(runtimes []airuntime.Runtime, model string) string {
	for _, rt := range runtimes {
		for _, mo := range rt.Models {
			if modelMatches(mo.Name, model) {
				return rt.Endpoint
			}
		}
	}
	return ""
}

// LocalModelFor is LocalRuntimeFor plus the model as the runtime reports it,
// so the serving agent can check it is the approved one.
func LocalModelFor(runtimes []airuntime.Runtime, model string) (string, airuntime.Model, bool) {
	for _, rt := range runtimes {
		for _, mo := range rt.Models {
			if modelMatches(mo.Name, model) {
				return rt.Endpoint, mo, true
			}
		}
	}
	return "", airuntime.Model{}, false
}
