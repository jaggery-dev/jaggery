package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

// A house where the laptop has an approved llama and a model nobody approved.
func houseWithModels(t *testing.T) (dir string, a *home.Approvals) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	laptop := house.Machine{MachineID: "d47f", Name: "laptop", Trust: home.Approved, Reachable: true,
		Inventory: inventory.Report{Runtimes: []airuntime.Runtime{{Kind: airuntime.Ollama, Models: []airuntime.Model{
			{Name: "llama3.2:3b", Digest: "a80c"},
			{Name: "qwen2.5:0.5b", Digest: "500a"},
		}}}}}
	if err := house.Save(dir, house.Snapshot{Self: house.Machine{MachineID: "71df", Name: "desktop", Self: true, Reachable: true}, Peers: []house.Machine{laptop}}); err != nil {
		t.Fatal(err)
	}
	a = home.OpenApprovals(dir)
	if err := a.Start([]home.Approval{{Name: "llama3.2:3b", Digest: "a80c", By: "baseline"}}); err != nil {
		t.Fatal(err)
	}
	return dir, a
}

func TestApprovedOnlyHidesPendingModelsWithoutTouchingTheHouse(t *testing.T) {
	_, a := houseWithModels(t)
	machines := loadHouse().All()
	filtered := approvedOnly(machines, a)
	if got := filtered[1].Inventory.Runtimes[0].Models; len(got) != 1 || got[0].Name != "llama3.2:3b" {
		t.Fatalf("filtered models: %+v", got)
	}
	if got := machines[1].Inventory.Runtimes[0].Models; len(got) != 2 {
		t.Fatalf("filtering changed the snapshot itself: %+v", got)
	}
}

// The G4 done-when: an unapproved model can't be used through ai.local, and
// the refusal says how to approve it.
func TestRouterRefusesAPendingModel(t *testing.T) {
	_, a := houseWithModels(t)
	saved := approvalList
	approvalList = a
	defer func() { approvalList = saved }()

	store, _ := home.Open(t.TempDir())
	mux := http.NewServeMux()
	registerRouter(mux, newIdentity(t, "desktop"), store)

	ask := func(model string) (int, string) {
		body, _ := json.Marshal(map[string]any{"model": model, "messages": []any{}})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
		return rec.Code, rec.Body.String()
	}
	if code, body := ask("qwen2.5:0.5b"); code != http.StatusForbidden || !strings.Contains(body, "jag models approve qwen2.5:0.5b") {
		t.Fatalf("pending model: %d %s", code, body)
	}
	if code, _ := ask("no-such-model"); code != http.StatusNotFound {
		t.Fatalf("unknown model: %d, want 404", code)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if strings.Contains(rec.Body.String(), "qwen2.5") || !strings.Contains(rec.Body.String(), "llama3.2:3b") {
		t.Fatalf("/v1/models should list only approved models: %s", rec.Body.String())
	}
}

func TestApproveModelPinsTheCopyInTheHouse(t *testing.T) {
	_, a := houseWithModels(t)
	if _, _, err := identity.LoadOrCreate(); err != nil {
		t.Fatal(err)
	}
	if err := approveModel([]string{"qwen2.5:0.5b"}); err != nil {
		t.Fatal(err)
	}
	if !a.Approved("qwen2.5:0.5b", "500a") {
		t.Fatal("approve didn't pin the house's copy")
	}
	if err := approveModel([]string{"qwen2.5:0.5b"}); err == nil {
		t.Fatal("approving an already-approved model should say there's nothing to approve")
	}
}
