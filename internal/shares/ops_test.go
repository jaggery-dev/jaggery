package shares

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T, writable bool) (state, folder string) {
	t.Helper()
	state, folder = t.TempDir(), t.TempDir()
	files := map[string]string{
		"notes/plan.md":     "# Plan\nbuy a NAS\nback up photos\n",
		"notes/todo.txt":    "fix the boiler\n",
		"photo.png":         "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
		"report.pdf":        "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n",
		".env":              "TOKEN=nope",
		"keys/server.key":   "nope",
		"notes/.git/config": "nope",
	}
	for name, body := range files {
		p := filepath.Join(folder, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(state, folder, "docs", writable, time.Now()); err != nil {
		t.Fatal(err)
	}
	return state, folder
}

func do(t *testing.T, state string, req Request) Response {
	t.Helper()
	req.Share = "docs"
	resp, err := Do(context.Background(), state, req, time.Now())
	if err != nil {
		t.Fatalf("%s %s: %v", req.Op, req.Path, err)
	}
	return resp
}

func TestListHidesExcluded(t *testing.T) {
	state, _ := setup(t, false)
	got := do(t, state, Request{Op: OpList})
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "keys,notes,photo.png,report.pdf" {
		t.Fatalf("root lists %v", names)
	}
	got = do(t, state, Request{Op: OpList, Path: "keys"})
	if len(got.Entries) != 0 {
		t.Fatalf("keys lists %+v", got.Entries)
	}
	got = do(t, state, Request{Op: OpList, Path: "notes"})
	if len(got.Entries) != 2 || got.Entries[0].Name != "plan.md" || got.Entries[0].Size == 0 {
		t.Fatalf("notes lists %+v", got.Entries)
	}
}

func TestReadKinds(t *testing.T) {
	state, _ := setup(t, false)
	for p, want := range map[string]struct {
		mime string
		text bool
	}{
		"notes/plan.md": {"text/markdown", true},
		"photo.png":     {"image/png", false},
		"report.pdf":    {"application/pdf", false},
	} {
		got := do(t, state, Request{Op: OpRead, Path: p})
		if got.MIME != want.mime || got.Text != want.text || got.Size != int64(len(got.Data)) {
			t.Errorf("%s: mime %q text %v size %d data %d", p, got.MIME, got.Text, got.Size, len(got.Data))
		}
	}
	for _, p := range []string{".env", "keys/server.key", "notes/.git/config", "../../etc/passwd"} {
		if _, err := Do(context.Background(), state, Request{Op: OpRead, Share: "docs", Path: p}, time.Now()); err == nil {
			t.Errorf("read %s", p)
		}
	}
}

// Chunked reads of text never cut a character in half, and together they
// give back the whole file.
func TestReadChunksKeepCharacters(t *testing.T) {
	state, folder := setup(t, false)
	body := strings.Repeat("naïve café ✓ ", 50)
	if err := os.WriteFile(filepath.Join(folder, "u.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for off := int64(0); off < int64(len(body)); {
		got := do(t, state, Request{Op: OpRead, Path: "u.txt", Offset: off, Length: 7})
		if !got.Text || got.Offset != off {
			t.Fatalf("chunk at %d: text %v offset %d", off, got.Text, got.Offset)
		}
		if len(got.Data) == 0 {
			t.Fatalf("empty chunk at %d", off)
		}
		out.Write(got.Data)
		off += int64(len(got.Data))
	}
	if out.String() != body {
		t.Fatal("chunks don't add up to the file")
	}
}

func TestSearch(t *testing.T) {
	state, _ := setup(t, false)
	got := do(t, state, Request{Op: OpSearch, Query: "NAS"})
	if len(got.Matches) != 1 || got.Matches[0] != (Match{Path: "notes/plan.md", Line: 2, Text: "buy a NAS"}) {
		t.Fatalf("matches %+v", got.Matches)
	}
	got = do(t, state, Request{Op: OpSearch, Query: "todo"})
	if len(got.Matches) != 1 || got.Matches[0].Path != "notes/todo.txt" || got.Matches[0].Line != 0 {
		t.Fatalf("name matches %+v", got.Matches)
	}
	if got := do(t, state, Request{Op: OpSearch, Query: "nope"}); len(got.Matches) != 0 {
		t.Fatalf("search found excluded files: %+v", got.Matches)
	}
}

func TestWritesNeedWritable(t *testing.T) {
	state, folder := setup(t, false)
	for _, op := range []string{OpWrite, OpDelete} {
		_, err := Do(context.Background(), state, Request{Op: op, Share: "docs", Path: "notes/todo.txt", Data: []byte("x")}, time.Now())
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("%s on a read-only share: %v", op, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(folder, "notes", "todo.txt")); string(b) != "fix the boiler\n" {
		t.Fatal("a read-only share was changed")
	}
}

func TestWriteKeepsOldVersion(t *testing.T) {
	state, folder := setup(t, true)
	got := do(t, state, Request{Op: OpWrite, Path: "notes/todo.txt", Data: []byte("boiler fixed\n")})
	if got.Version == "" || got.Written != 13 {
		t.Fatalf("write: %+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(folder, "notes", "todo.txt")); string(b) != "boiler fixed\n" {
		t.Fatalf("file now %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(folder, filepath.FromSlash(got.Version))); string(b) != "fix the boiler\n" {
		t.Fatalf("old version %q", b)
	}
	if got := do(t, state, Request{Op: OpList, Path: "notes"}); len(got.Entries) != 2 {
		t.Fatalf("versions or temp files are listed: %+v", got.Entries)
	}

	// A new file in a new folder.
	got = do(t, state, Request{Op: OpWrite, Path: "summaries/plan.md", Data: []byte("short")})
	if got.Version != "" {
		t.Fatalf("a new file has an old version: %+v", got)
	}

	// Delete moves the file into the versions folder.
	got = do(t, state, Request{Op: OpDelete, Path: "summaries/plan.md"})
	if _, err := os.Stat(filepath.Join(folder, "summaries", "plan.md")); !os.IsNotExist(err) {
		t.Fatal("deleted file still there")
	}
	if b, _ := os.ReadFile(filepath.Join(folder, filepath.FromSlash(got.Version))); string(b) != "short" {
		t.Fatalf("deleted file's version %q", b)
	}
	if _, err := Do(context.Background(), state, Request{Op: OpDelete, Share: "docs", Path: "notes"}, time.Now()); err == nil {
		t.Fatal("deleted a folder")
	}
	if _, err := Do(context.Background(), state, Request{Op: OpWrite, Share: "docs", Path: VersionsDir + "/x"}, time.Now()); err == nil {
		t.Fatal("wrote into the versions folder")
	}
	if _, err := Do(context.Background(), state, Request{Op: OpWrite, Share: "docs", Path: "big", Data: make([]byte, MaxWrite+1)}, time.Now()); !errors.Is(err, ErrTooBig) {
		t.Fatalf("oversized write: %v", err)
	}
}

func TestOldVersionsArePruned(t *testing.T) {
	state, folder := setup(t, true)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, day := range []int{0, 20, 45} {
		_, err := Do(context.Background(), state, Request{Op: OpWrite, Share: "docs", Path: "notes/todo.txt", Data: []byte{byte('a' + i)}}, start.AddDate(0, 0, day))
		if err != nil {
			t.Fatal(err)
		}
	}
	vs, err := os.ReadDir(filepath.Join(folder, VersionsDir, "notes", "todo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Day 45 prunes day 0's version (the original), keeping days 20 and 45.
	var names []string
	for _, v := range vs {
		names = append(names, path.Base(v.Name()))
	}
	if len(names) != 2 || !strings.HasPrefix(names[0], "20260121") || !strings.HasPrefix(names[1], "20260215") {
		t.Fatalf("versions kept: %v", names)
	}
}

func TestFileErrorsArePlain(t *testing.T) {
	state, folder := setup(t, true)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(folder, "escape.txt")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	for _, c := range []struct {
		op, path, want string
	}{
		{OpRead, "missing.md", `no such file in the share "docs": missing.md`},
		{OpRead, "../../etc/passwd", `no such file in the share "docs": ../../etc/passwd`},
		{OpList, "photos", `no such file in the share "docs": photos`},
		{OpDelete, "gone.txt", `no such file in the share "docs": gone.txt`},
		{OpRead, "escape.txt", `a link that leads out of the share "docs": escape.txt`},
	} {
		_, err := Do(context.Background(), state, Request{Op: c.op, Share: "docs", Path: c.path}, time.Now())
		var fe *FileError
		if err == nil || err.Error() != c.want || !errors.As(err, &fe) || fe.Err == nil {
			t.Errorf("%s %s: %v, want %q", c.op, c.path, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), outside) {
			t.Errorf("%s %s names the folder outside: %v", c.op, c.path, err)
		}
	}
}
