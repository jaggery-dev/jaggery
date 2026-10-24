package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendThenReadADay(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, m := range []string{"llama3.2:3b", "qwen3:8b"} {
		if err := l.Append(Entry{At: now, Device: "Alex-iPhone.local", Model: m, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Append(Entry{At: now.Add(-48 * time.Hour), Model: "old"}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Day(now)
	if err != nil || len(got) != 2 || got[1].Model != "qwen3:8b" {
		t.Fatalf("today: got %+v, %v", got, err)
	}
	if none, err := l.Day(now.Add(24 * time.Hour)); err != nil || len(none) != 0 {
		t.Fatalf("a day with no requests: got %+v, %v", none, err)
	}
}

func TestPruneKeepsRetention(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	now := time.Now()
	_ = l.Append(Entry{At: now, Model: "today"})
	_ = l.Append(Entry{At: now.Add(-Retention - 48*time.Hour), Model: "ancient"})
	l.Prune(now)
	files, _ := os.ReadDir(filepath.Join(dir, dirName))
	if len(files) != 1 {
		t.Fatalf("%d files left, want 1", len(files))
	}
}

func TestMergeIsNewestFirst(t *testing.T) {
	t0 := time.Now()
	got := Merge([]Entry{{At: t0, Model: "a"}}, []Entry{{At: t0.Add(time.Minute), Model: "b"}, {At: t0.Add(-time.Minute), Model: "c"}})
	if got[0].Model != "b" || got[1].Model != "a" || got[2].Model != "c" {
		t.Fatalf("got %+v", got)
	}
}

func TestAppName(t *testing.T) {
	cases := map[string]string{
		"OpenAI/Python 1.51.0":                     "OpenAI Python SDK",
		"Python/3.11 aiohttp/3.9.5":                "Python (aiohttp)",
		"curl/8.9.1":                               "curl",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)": "browser",
		"goose/1.9.0":                              "Goose",
		"SomethingNew/0.1 (+https://example.com)":  "SomethingNew/0.1",
		"": "",
	}
	for ua, want := range cases {
		if got := AppName(ua); got != want {
			t.Errorf("AppName(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestParseDay(t *testing.T) {
	now := time.Now()
	if d, err := ParseDay("", now); err != nil || !d.Equal(now) {
		t.Fatalf("empty: %v, %v", d, err)
	}
	if d, err := ParseDay("2026-10-07", now); err != nil || d.Day() != 7 {
		t.Fatalf("a date: %v, %v", d, err)
	}
	if _, err := ParseDay("yesterday", now); err == nil {
		t.Fatal("accepted a word")
	}
}

// The dashboard reads the merged list; a quiet day must be [], not null.
func TestMergeOfNothingIsAnEmptyList(t *testing.T) {
	raw, _ := json.Marshal(Merge(nil, nil))
	if string(raw) != "[]" {
		t.Fatalf("got %s, want []", raw)
	}
}
