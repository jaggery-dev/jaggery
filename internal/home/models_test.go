package home

import (
	"errors"
	"testing"
	"time"
)

func TestApprovalsBeforeStartServeEverything(t *testing.T) {
	a := OpenApprovals(t.TempDir())
	if a.Started() || a.Enforcing() || !a.Approved("anything", "x") {
		t.Fatal("a house without approval should serve every model")
	}
	if _, err := a.Add(Approval{Name: "m"}); !errors.Is(err, ErrNoApprovals) {
		t.Fatalf("Add before Start: %v", err)
	}
}

func TestApprovalPinsTheDigest(t *testing.T) {
	a := OpenApprovals(t.TempDir())
	if err := a.Start([]Approval{{Name: "llama3.2:3b", Digest: "a80c", By: "baseline"}, {Name: "llama3.2:3b", Digest: "a80c"}}); err != nil {
		t.Fatal(err)
	}
	if !a.Enforcing() || !a.Approved("llama3.2:3b", "a80c") {
		t.Fatal("the baseline isn't approved")
	}
	if len(a.All()) != 1 {
		t.Fatalf("baseline not deduplicated: %+v", a.All())
	}
	if a.Approved("llama3.2:3b", "ffff") {
		t.Fatal("a swapped file under an approved name was approved")
	}
	if a.Approved("qwen3:8b", "500a") {
		t.Fatal("a new model was approved without anyone approving it")
	}

	n, err := a.Add(Approval{Name: "qwen3:8b", Digest: "500a", At: time.Now(), By: "desktop"}, Approval{Name: "llama3.2:3b", Digest: "a80c"})
	if err != nil || n != 1 {
		t.Fatalf("Add: %d new, %v; want 1", n, err)
	}
	if !a.Approved("qwen3:8b", "500a") {
		t.Fatal("approval didn't take")
	}

	if err := a.Start(nil); err != nil || !a.Approved("qwen3:8b", "500a") {
		t.Fatal("a second Start must not wipe approvals")
	}
	if err := a.SetEnforce(false); err != nil || !a.Approved("anything", "") {
		t.Fatal("with enforcement off every model is served")
	}
}
