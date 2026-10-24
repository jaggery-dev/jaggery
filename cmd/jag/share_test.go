package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/shares"
)

// The house learns a share's name and mode from the inventory, never its path.
func TestInventoryCarriesShareNamesNotPaths(t *testing.T) {
	state, docs := t.TempDir(), t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", state)
	if _, err := shares.Add(state, docs, "docs", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	r := collectInventory()
	if len(r.Shares) != 1 || r.Shares[0].Name != "docs" || !r.Shares[0].Writable {
		t.Fatalf("shares = %+v", r.Shares)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), docs) {
		t.Fatal("the inventory carries the share's path")
	}
}
