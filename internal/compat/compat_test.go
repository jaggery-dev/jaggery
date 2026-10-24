package compat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProtocolRangesMustOverlap(t *testing.T) {
	cases := []struct {
		min, max int
		want     bool
	}{
		{0, 0, true},  // a build from before the protocol was reported speaks 1
		{1, 1, true},  // today
		{1, 2, true},  // a newer peer that still accepts ours
		{2, 3, false}, // a newer peer that dropped ours
	}
	for _, c := range cases {
		if got := Compatible(c.min, c.max); got != c.want {
			t.Errorf("Compatible(%d, %d) = %v, want %v", c.min, c.max, got, c.want)
		}
	}
}

func TestNewStateIsStamped(t *testing.T) {
	dir := t.TempDir()
	if err := PrepareState(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	var st state
	raw, _ := os.ReadFile(filepath.Join(dir, stateFile))
	if json.Unmarshal(raw, &st) != nil || st.Schema != StateSchema || st.MinReader != 1 {
		t.Fatalf("stamped %s", raw)
	}
}

// After a rollback, an older build must refuse state it can't read rather
// than misread it, and say how to recover.
func TestStateFromANewerBuildIsRefused(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, stateFile), []byte(`{"schema":7,"min_reader":7}`), 0o600)
	err := PrepareState(dir, time.Now())
	if !errors.Is(err, ErrNewerState) {
		t.Fatalf("got %v, want ErrNewerState", err)
	}
}

// A newer schema that older builds can still read (no breaking migration)
// is fine.
func TestNewerButReadableStateIsAccepted(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, stateFile), []byte(`{"schema":3,"min_reader":1}`), 0o600)
	if err := PrepareState(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Migrating forward backs the state up first, and a breaking migration
// raises the floor for readers.
func TestMigrationBacksUpAndRaisesTheFloor(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, stateFile), []byte(`{"schema":0,"min_reader":0}`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "peers.json"), []byte(`{"peers":{}}`), 0o600)
	ran := false
	saved := Migrations
	Migrations = []Migration{{From: 0, Breaking: true, Run: func(string) error { ran = true; return nil }}}
	defer func() { Migrations = saved }()

	if err := PrepareState(dir, time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the migration didn't run")
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", "schema-0-20261007-180000", "peers.json")); err != nil {
		t.Fatalf("no backup: %v", err)
	}
	var st state
	raw, _ := os.ReadFile(filepath.Join(dir, stateFile))
	_ = json.Unmarshal(raw, &st)
	if st.Schema != 1 || st.MinReader != 1 {
		t.Fatalf("after migrating: %s", raw)
	}
}
