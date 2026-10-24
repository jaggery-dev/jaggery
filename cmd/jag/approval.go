package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/modelwatch"
)

const approvalsPath = "/v1/models/approvals"

// approvalList is the running agent's; nil in the CLI and in tests that
// don't set one, which means every model is served.
var approvalList *home.Approvals

// startApprovals begins model approval on a machine that hasn't yet: the
// models it has now, and every model its ledger has seen, are the baseline,
// so turning approval on breaks nobody's chat.
func startApprovals(dir string, lg *modelwatch.Ledger) *home.Approvals {
	a := home.OpenApprovals(dir)
	if a.Started() {
		return a
	}
	now := time.Now().UTC()
	var baseline []home.Approval
	for _, rt := range airuntime.Detect(context.Background()) {
		for _, m := range rt.Models {
			baseline = append(baseline, home.Approval{Name: m.Name, Digest: m.Digest, At: now, By: "baseline"})
		}
	}
	for _, r := range lg.Records() {
		baseline = append(baseline, home.Approval{Name: r.Name, Digest: r.Digest, At: now, By: "baseline"})
	}
	if err := a.Start(baseline); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not start model approval:", err)
	} else {
		fmt.Printf("model approval on: %d model(s) already here are approved; new ones wait for `jag models approve`\n", len(baseline))
	}
	return a
}

// approvedOnly is the house with every unapproved model taken out, which is
// what the router may route to.
func approvedOnly(machines []house.Machine, a *home.Approvals) []house.Machine {
	if a == nil || !a.Enforcing() {
		return machines
	}
	out := make([]house.Machine, len(machines))
	for i, m := range machines {
		m.Inventory.Runtimes = append([]airuntime.Runtime(nil), m.Inventory.Runtimes...)
		for j, rt := range m.Inventory.Runtimes {
			var kept []airuntime.Model
			for _, mo := range rt.Models {
				if a.Approved(mo.Name, mo.Digest) {
					kept = append(kept, mo)
				}
			}
			m.Inventory.Runtimes[j].Models = kept
		}
		out[i] = m
	}
	return out
}

// markPending flags this machine's unapproved models in its inventory.
func markPending(runtimes []airuntime.Runtime) {
	if approvalList == nil || !approvalList.Enforcing() {
		return
	}
	for i := range runtimes {
		for j := range runtimes[i].Models {
			m := &runtimes[i].Models[j]
			m.Pending = !approvalList.Approved(m.Name, m.Digest)
		}
	}
}

// approvalsHandler shares this machine's approvals with approved peers.
func approvalsHandler(w http.ResponseWriter, r *http.Request) {
	list := []home.Approval{}
	if approvalList != nil {
		list = append(list, approvalList.All()...)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// syncApprovals takes in every reachable approved peer's approvals, so one
// approval anywhere covers the house. Approvals only accumulate.
func syncApprovals(ctx context.Context, id *identity.Identity, store *home.Store) {
	if approvalList == nil {
		return
	}
	peers, err := store.List()
	if err != nil {
		return
	}
	for _, p := range peers {
		if p.Trust != home.Approved || !p.Verified() || len(p.Addrs) == 0 {
			continue
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		var list []home.Approval
		err := peerGet(fetchCtx, id, p, approvalsPath, func(body io.Reader) error {
			return json.NewDecoder(io.LimitReader(body, 4<<20)).Decode(&list)
		})
		cancel()
		if err != nil {
			continue // offline, or older than G4
		}
		if n, err := approvalList.Add(list...); err == nil && n > 0 {
			fmt.Printf("%d model approval(s) from %s\n", n, p.Name)
		}
	}
}

const modelsHelp = `usage: jag models                         every model in the house, and whether it's approved
       jag models approve <name> [digest]  let the router serve a model (its current files)
       jag models enforce on|off            refuse unapproved models (on), or serve every model

Approval is house-wide: approve on any machine and the others follow within
seconds. It pins the model's digest, so if the files under a name change, the
model waits for approval again.
`

func models(args []string) error {
	if len(args) == 0 {
		return listModels()
	}
	if jsonOutput && args[0] != "-h" && args[0] != "--help" && args[0] != "help" {
		return errJSONNotHere("jag models " + args[0])
	}
	switch args[0] {
	case "approve":
		return approveModel(args[1:])
	case "enforce":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			return usagef("usage: jag models enforce on|off")
		}
		dir, err := identity.StateDir()
		if err != nil {
			return err
		}
		if err := home.OpenApprovals(dir).SetEnforce(args[1] == "on"); err != nil {
			return fmt.Errorf("%w: start the agent (`jag up`) once first", err)
		}
		fmt.Printf("model approval %s on this machine\n", args[1])
		return nil
	case "-h", "--help", "help":
		fmt.Print(modelsHelp)
		return nil
	}
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, modelsHelp) }
	return parseFlags(fs, args)
}

// modelJSON is one copy of a model in `jag models --json`.
type modelJSON struct {
	Name     string         `json:"name"`
	Machine  string         `json:"machine"`
	Runtime  airuntime.Kind `json:"runtime"`
	Format   string         `json:"format,omitempty"`
	Digest   string         `json:"digest,omitempty"`
	Loaded   bool           `json:"loaded,omitempty"`
	Approved bool           `json:"approved"` // the router serves it
}

type houseModel struct {
	machine string
	runtime airuntime.Kind
	model   airuntime.Model
}

func houseModels(snap house.Snapshot) []houseModel {
	var out []houseModel
	for _, m := range snap.All() {
		for _, rt := range m.Inventory.Runtimes {
			for _, mo := range rt.Models {
				out = append(out, houseModel{m.Name, rt.Kind, mo})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].model.Name < out[j].model.Name })
	return out
}

func listModels() error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	a := home.OpenApprovals(dir)
	all := houseModels(loadHouse())
	if jsonOutput {
		list := make([]modelJSON, 0, len(all))
		for _, hm := range all {
			list = append(list, modelJSON{Name: hm.model.Name, Machine: hm.machine, Runtime: hm.runtime,
				Format: hm.model.Format, Digest: hm.model.Digest, Loaded: hm.model.Loaded,
				Approved: a.Approved(hm.model.Name, hm.model.Digest)})
		}
		return emitJSON(map[string]any{"approval_enforced": a.Enforcing(), "models": list})
	}
	if len(all) == 0 {
		fmt.Println("no models in the house yet")
		return nil
	}
	if !a.Enforcing() {
		fmt.Println("model approval is off: every model is served")
	}
	for _, hm := range all {
		state := "approved"
		if !a.Approved(hm.model.Name, hm.model.Digest) {
			state = "PENDING"
		}
		fmt.Printf("%-9s %-24s on %-22s %s %s %s\n", state, hm.model.Name, hm.machine, hm.runtime,
			orUnknown(hm.model.Format), shortDigest(hm.model.Digest))
	}
	return nil
}

func approveModel(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usagef("usage: jag models approve <name> [digest]")
	}
	name, prefix := args[0], ""
	if len(args) == 2 {
		prefix = args[1]
	}
	id, err := identity.Load()
	if err != nil {
		return err
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	a := home.OpenApprovals(dir)

	// Every distinct copy of that name in the house that isn't approved yet.
	digests := map[string][]string{}
	for _, hm := range houseModels(loadHouse()) {
		if hm.model.Name != name || a.Approved(hm.model.Name, hm.model.Digest) {
			continue
		}
		if prefix != "" && !strings.HasPrefix(hm.model.Digest, prefix) {
			continue
		}
		digests[hm.model.Digest] = append(digests[hm.model.Digest], hm.machine)
	}
	switch {
	case len(digests) == 0:
		return fmt.Errorf("no model %q waiting for approval (see `jag models`)", name)
	case len(digests) > 1:
		var lines []string
		for d, machines := range digests {
			lines = append(lines, fmt.Sprintf("  %s on %s", shortDigest(d), strings.Join(machines, ", ")))
		}
		sort.Strings(lines)
		return fmt.Errorf("different files are called %q on different machines; approve one by digest:\n%s", name, strings.Join(lines, "\n"))
	}
	for d, machines := range digests {
		if _, err := a.Add(home.Approval{Name: name, Digest: d, At: time.Now().UTC(), By: id.Name}); err != nil {
			return fmt.Errorf("%w: start the agent (`jag up`) once first", err)
		}
		fmt.Printf("approved %s (digest %s) on %s; the rest of the house follows within seconds\n", name, shortDigest(d), strings.Join(machines, ", "))
	}
	return nil
}
