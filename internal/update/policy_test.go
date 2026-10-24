package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsFollowTheBuild(t *testing.T) {
	cases := map[string]Settings{
		"beta": {Auto, "beta"},
		"prod": {Notify, "prod"},
		"":     {Notify, "beta"}, // a dev build is never replaced unasked
	}
	for build, want := range cases {
		if got := LoadSettings(t.TempDir(), build); got != want {
			t.Errorf("build channel %q: got %+v, want %+v", build, got, want)
		}
	}
}

func TestSettingsRoundTripAndRejectNonsense(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSettings(dir, Settings{Policy: Off, Channel: "prod"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(dir, "beta"); got != (Settings{Off, "prod"}) {
		t.Fatalf("got %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "update.json"), []byte(`{"policy":"yolo","channel":"nightly"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(dir, "beta"); got != (Settings{Auto, "beta"}) {
		t.Fatalf("a bad file should fall back to the defaults, got %+v", got)
	}
	if _, err := ParsePolicy("sometimes"); err == nil {
		t.Fatal("accepted a made-up policy")
	}
}

func TestBadBuildsAreRememberedOnce(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []string{"aaa", "bbb", "aaa"} {
		if err := MarkBad(dir, c); err != nil {
			t.Fatal(err)
		}
	}
	if got := Bad(dir); len(got) != 2 || got[0] != "aaa" || got[1] != "bbb" {
		t.Fatalf("got %v", got)
	}
}

func TestPendingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadPending(dir); ok {
		t.Fatal("pending without a marker")
	}
	if err := SavePending(dir, Pending{From: "aaa", Attempts: 2}); err != nil {
		t.Fatal(err)
	}
	if p, ok := LoadPending(dir); !ok || p.From != "aaa" || p.Attempts != 2 {
		t.Fatalf("got %+v, %v", p, ok)
	}
	ClearPending(dir)
	if _, ok := LoadPending(dir); ok {
		t.Fatal("marker survived ClearPending")
	}
}
