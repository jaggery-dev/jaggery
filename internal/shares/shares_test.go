package shares

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddListRemove(t *testing.T) {
	state, docs := t.TempDir(), t.TempDir()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	s, err := Add(state, docs, "docs", true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Writable || s.Name != "docs" {
		t.Fatalf("got %+v", s)
	}
	if _, err := Add(state, docs, "docs", false, now); err == nil {
		t.Fatal("a second share with the same name was accepted")
	}
	inner := filepath.Join(docs, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(state, inner, "inner", false, now); err == nil {
		t.Fatal("a share inside another share was accepted")
	}

	infos := Infos(state, now)
	if len(infos) != 1 || infos[0].Name != "docs" || !infos[0].Writable || infos[0].Use != nil {
		t.Fatalf("infos = %+v", infos)
	}
	if err := Remove(state, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(state, "docs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing twice: %v", err)
	}
	if _, err := os.Stat(docs); err != nil {
		t.Fatal("removing a share touched the folder")
	}
}

func TestAddRefuses(t *testing.T) {
	state := t.TempDir()
	now := time.Now()
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ folder, name string }{
		"a file":          {f, "f"},
		"the root":        {"/", "root"},
		"the state dir":   {state, "state"},
		"a bad name":      {t.TempDir(), "Has Spaces"},
		"a missing path":  {filepath.Join(state, "nope"), "nope"},
		"inside state":    {mkdir(t, state, "sub"), "sub"},
		"a too long name": {t.TempDir(), "abcdefghijklmnopqrstuvwxyz0123456789"},
	} {
		if _, err := Add(state, tc.folder, tc.name, false, now); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func mkdir(t *testing.T, parent, name string) string {
	p := filepath.Join(parent, name)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClean(t *testing.T) {
	ok := map[string]string{
		"":                 ".",
		"/":                ".",
		"notes/a.md":       "notes/a.md",
		"/notes/a.md":      "notes/a.md",
		"../../etc/passwd": "etc/passwd",
		"notes/../a.md":    "a.md",
		"tax 2025/p60.pdf": "tax 2025/p60.pdf",
	}
	for in, want := range ok {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{".git/config", "a/.ssh/id_ed25519", ".env", "keys/server.KEY", "tls/cert.pem", VersionsDir + "/x", `a\b`, "a\x00b"} {
		if got, err := Clean(in); err == nil {
			t.Errorf("Clean(%q) = %q, want refused", in, got)
		}
	}
}

// The S1 done-when: a symlink leading outside the share is refused.
func TestRootRefusesEscapingSymlink(t *testing.T) {
	state, docs, outside := t.TempDir(), t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.txt"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(docs, "link.txt")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := os.Symlink("a.txt", filepath.Join(docs, "inside.txt")); err != nil {
		t.Fatal(err)
	}
	s, err := Add(state, docs, "docs", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if b, err := root.ReadFile("a.txt"); err != nil || string(b) != "yes" {
		t.Fatalf("reading a.txt: %q, %v", b, err)
	}
	if b, err := root.ReadFile("inside.txt"); err != nil || string(b) != "yes" {
		t.Fatalf("a symlink inside the share: %q, %v", b, err)
	}
	if b, err := root.ReadFile("link.txt"); err == nil {
		t.Fatalf("read %q through a symlink leading outside the share", b)
	}
}
