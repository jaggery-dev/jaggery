package shares

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogAndUse(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for _, e := range []LogEntry{
		{Op: OpRead, Share: "docs", Path: "a.md", App: "agent-one", Bytes: 10},
		{Op: OpRead, Share: "docs", Path: "b.md", App: "agent-two", Bytes: 5},
		{Op: OpWrite, Share: "docs", Path: "a.md", App: "agent-one", Error: "this share is read-only"},
		{Op: OpSearch, Share: "docs", Query: "boiler", App: "agent-one"},
		{Op: OpWrite, Share: "summaries", Path: "x.md", App: "agent-one", ServedBy: "nas"},
	} {
		e.Time = now
		if err := AppendLog(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadLog(dir, now)
	if err != nil || len(got) != 5 {
		t.Fatalf("read %d entries, %v", len(got), err)
	}
	if info, _ := os.Stat(logPath(dir, now)); info.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode %v", info.Mode().Perm())
	}
	use := UseToday(dir, now)
	d := use["docs"]
	if d == nil || d.Reads != 2 || d.Refused != 1 || d.Searches != 1 || d.Writes != 0 {
		t.Fatalf("docs use %+v", d)
	}
	if len(d.Apps) != 2 || d.Apps[0] != "agent-one" {
		t.Fatalf("apps %v", d.Apps)
	}
	if use["summaries"] != nil {
		t.Fatal("a share served by another machine was counted here")
	}
}

func TestLogPrunesOldDays(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -40)
	if err := AppendLog(dir, LogEntry{Time: old, Op: OpRead, Share: "docs"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLog(dir, LogEntry{Time: time.Now(), Op: OpRead, Share: "docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath(dir, old)); !os.IsNotExist(err) {
		t.Fatal("a 40-day-old log survived")
	}
}

func TestRestore(t *testing.T) {
	state, folder := setup(t, true)
	file := filepath.Join(folder, "notes", "todo.txt")
	ctx := context.Background()
	t0 := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	for i, body := range []string{"second", "third"} {
		if _, err := Do(ctx, state, Request{Op: OpWrite, Share: "docs", Path: "notes/todo.txt", Data: []byte(body)}, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Do(ctx, state, Request{Op: OpDelete, Share: "docs", Path: "notes/todo.txt"}, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	vs, err := Versions(state, "docs", "notes/todo.txt")
	if err != nil || len(vs) != 3 {
		t.Fatalf("%d versions, %v", len(vs), err)
	}

	// The newest version is the deleted file.
	resp, err := Restore(state, "docs", "notes/todo.txt", "", t0.Add(3*time.Minute))
	if err != nil || resp.Version != "" {
		t.Fatalf("restore: %+v %v", resp, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "third" {
		t.Fatalf("restored %q", b)
	}

	// A named version, over the current file, which is kept.
	resp, err = Restore(state, "docs", "notes/todo.txt", vs[2].Name, t0.Add(4*time.Minute))
	if err != nil || resp.Version == "" || resp.OldSHA256 == "" || resp.NewSHA256 == "" {
		t.Fatalf("restore: %+v %v", resp, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "fix the boiler\n" {
		t.Fatalf("restored %q", b)
	}
	if vs, _ := Versions(state, "docs", "notes/todo.txt"); len(vs) != 4 {
		t.Fatalf("the replaced file wasn't kept: %d versions", len(vs))
	}
	if _, err := Restore(state, "docs", "notes/plan.md", "", time.Now()); err == nil {
		t.Fatal("restored a file with no versions")
	}
	if _, err := Restore(state, "docs", "notes/todo.txt", "nope", time.Now()); err == nil {
		t.Fatal("restored a version that doesn't exist")
	}
}
