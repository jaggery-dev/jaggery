package shares

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits on one call. A model's context is the real limit on reads; agents
// read big files in chunks with Offset.
const (
	MaxRead        = 1 << 20
	MaxWrite       = 8 << 20
	MaxList        = 1000
	MaxMatches     = 100
	searchFiles    = 20000
	searchFileSize = 1 << 20
	searchTime     = 5 * time.Second
	// KeepVersions is how long an overwritten or deleted file's old version
	// is kept in VersionsDir.
	KeepVersions = 30 * 24 * time.Hour
)

// The operations a share supports.
const (
	OpList   = "list"
	OpRead   = "read"
	OpSearch = "search"
	OpWrite  = "write"
	OpDelete = "delete"
	// OpRestore is done at the holder's terminal (`jag files restore`),
	// never over the network.
	OpRestore = "restore"
)

// Request is one operation on a share. The same request is served locally or
// sent to the machine holding the share, which checks it against its own
// shares.json: a peer never decides whether a share is writable.
type Request struct {
	Op     string `json:"op"`
	Share  string `json:"share"`
	Path   string `json:"path,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Length int64  `json:"length,omitempty"`
	Query  string `json:"query,omitempty"`
	Data   []byte `json:"data,omitempty"`
}

// Entry is one item in a folder.
type Entry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir,omitempty"`
	Size     int64     `json:"size,omitempty"`
	MIME     string    `json:"mime,omitempty"`
	Modified time.Time `json:"modified"`
}

// Match is a search hit: a file whose name matches (Line 0), or a line in a
// text file.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

// Response is what an operation returns; which fields are set depends on Op.
type Response struct {
	Path    string  `json:"path,omitempty"`
	Entries []Entry `json:"entries,omitempty"`
	// Read: the bytes from Offset, the file's MIME type and total size, and
	// whether it is text (valid UTF-8, no NUL bytes).
	MIME   string `json:"mime,omitempty"`
	Text   bool   `json:"text,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Data   []byte `json:"data,omitempty"`
	// Search.
	Matches []Match `json:"matches,omitempty"`
	// Truncated: a list or search stopped at its limit.
	Truncated bool `json:"truncated,omitempty"`
	// Write and delete: where the old version went, inside VersionsDir;
	// empty when there was none.
	Version string `json:"version,omitempty"`
	Written int64  `json:"written,omitempty"`
	// OldSHA256 and NewSHA256: the file's contents before and after a write,
	// delete or restore (empty when there was no file).
	OldSHA256 string `json:"old_sha256,omitempty"`
	NewSHA256 string `json:"new_sha256,omitempty"`
}

var (
	// ErrReadOnly: a write or delete on a share not added with --writable.
	ErrReadOnly = errors.New("this share is read-only")
	// ErrTooBig: a write over MaxWrite.
	ErrTooBig = errors.New("too big")
)

// Do runs one operation on a share held by this machine.
func Do(ctx context.Context, dir string, req Request, now time.Time) (Response, error) {
	s, err := Find(dir, req.Share)
	if err != nil {
		return Response{}, err
	}
	rel, err := Clean(req.Path)
	if err != nil {
		return Response{}, err
	}
	root, err := s.Open()
	if err != nil {
		return Response{}, fmt.Errorf("opening share %q: %w", s.Name, err)
	}
	defer root.Close()

	var resp Response
	switch req.Op {
	case OpList:
		resp, err = list(root, rel)
	case OpRead:
		resp, err = read(root, rel, req.Offset, req.Length)
	case OpSearch:
		resp, err = search(ctx, root, rel, req.Query)
	case OpWrite, OpDelete:
		if !s.Writable {
			return Response{}, fmt.Errorf("%w (%q)", ErrReadOnly, s.Name)
		}
		if rel == "." {
			return Response{}, fmt.Errorf("%w: name a file", ErrBadPath)
		}
		if req.Op == OpWrite {
			resp, err = write(root, rel, req.Data, now)
		} else {
			resp, err = remove(root, rel, now)
		}
	default:
		return Response{}, fmt.Errorf("unknown operation %q", req.Op)
	}
	return resp, plainFileError(err, s.Name, req.Path)
}

// FileError is a file problem in plain words, such as `no such file in the
// share "notes": etc/passwd`. It names the share and the path as the caller
// gave it, not the operating system's error, which can
// hint at the folder outside the share. Err keeps that error for this
// machine's log.
type FileError struct {
	Share   string
	Path    string
	Problem string
	Err     error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("%s %q: %s", e.Problem, e.Share, e.Path)
}

func (e *FileError) Unwrap() error { return e.Err }

// plainFileError turns the operating system's file errors into FileErrors
// and leaves every other error as it is.
func plainFileError(err error, share, given string) error {
	var pe *fs.PathError
	if err == nil || !errors.As(err, &pe) {
		return err
	}
	problem := "can't open this in the share"
	switch {
	case errors.Is(err, fs.ErrNotExist):
		problem = "no such file in the share"
	case errors.Is(err, fs.ErrPermission):
		problem = "no permission to open this in the share"
	case pe.Err.Error() == "path escapes from parent":
		// os.Root refused a symlink that leads outside the share.
		problem = "a link that leads out of the share"
	}
	if given == "" {
		given = "."
	}
	return &FileError{Share: share, Path: given, Problem: problem, Err: err}
}

func list(root *os.Root, rel string) (Response, error) {
	f, err := root.Open(rel)
	if err != nil {
		return Response{}, err
	}
	defer f.Close()
	items, err := f.ReadDir(-1)
	if err != nil {
		return Response{}, err
	}
	resp := Response{Path: rel, Entries: []Entry{}}
	sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
	for _, it := range items {
		if Excluded(it.Name()) {
			continue
		}
		if len(resp.Entries) == MaxList {
			resp.Truncated = true
			break
		}
		e := Entry{Name: it.Name(), Dir: it.IsDir()}
		if info, err := it.Info(); err == nil {
			e.Modified = info.ModTime().UTC()
			if !e.Dir {
				e.Size = info.Size()
			}
		}
		if !e.Dir {
			e.MIME = mimeByName(e.Name)
		}
		resp.Entries = append(resp.Entries, e)
	}
	return resp, nil
}

func read(root *os.Root, rel string, offset, length int64) (Response, error) {
	f, err := root.Open(rel)
	if err != nil {
		return Response{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Response{}, err
	}
	if info.IsDir() {
		return Response{}, fmt.Errorf("%s is a folder: list it instead", rel)
	}
	size := info.Size()
	if offset < 0 || offset > size {
		return Response{}, fmt.Errorf("offset %d is outside the file (%d bytes)", offset, size)
	}
	if length <= 0 || length > MaxRead {
		length = MaxRead
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	mt := mimeOf(rel, head)
	text := looksText(head, int64(n) < size) && !strings.HasPrefix(mt, "image/")

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Response{}, err
	}
	buf := make([]byte, min(length, size-offset))
	n, err = io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Response{}, err
	}
	buf = buf[:n]
	if text {
		// Don't cut a character in half at either end of a chunk.
		for len(buf) > 0 && offset > 0 && !utf8.RuneStart(buf[0]) {
			buf, offset = buf[1:], offset+1
		}
		if end := offset + int64(len(buf)); end < size {
			buf = trimPartialRune(buf)
		}
	}
	return Response{Path: rel, MIME: mt, Text: text, Size: size, Offset: offset, Data: buf}, nil
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}

// looksText: valid UTF-8 with no NUL bytes. A sample cut from a longer file
// may end mid-character.
func looksText(sample []byte, cut bool) bool {
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	if cut {
		sample = trimPartialRune(sample)
	}
	return utf8.Valid(sample)
}

func mimeByName(name string) string {
	t := mime.TypeByExtension(path.Ext(name))
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return t
}

// mimeOf is the type by extension, else by content.
func mimeOf(name string, head []byte) string {
	if t := mimeByName(name); t != "" {
		return t
	}
	t := http.DetectContentType(head)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return t
}

func search(ctx context.Context, root *os.Root, rel, query string) (Response, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Response{}, errors.New("search needs a query")
	}
	ctx, cancel := context.WithTimeout(ctx, searchTime)
	defer cancel()
	resp := Response{Path: rel, Matches: []Match{}}
	files := 0
	stop := errors.New("stop")
	err := fs.WalkDir(root.FS(), rel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if p != rel && Excluded(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil || files >= searchFiles || len(resp.Matches) >= MaxMatches {
			resp.Truncated = true
			return stop
		}
		if p == rel {
			return nil
		}
		if strings.Contains(strings.ToLower(d.Name()), q) {
			resp.Matches = append(resp.Matches, Match{Path: p})
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		files++
		if info, err := d.Info(); err != nil || info.Size() > searchFileSize {
			return nil
		}
		return searchFile(root, p, q, &resp)
	})
	if err != nil && !errors.Is(err, stop) {
		return Response{}, err
	}
	return resp, nil
}

func searchFile(root *os.Root, p, q string, resp *Response) error {
	b, err := root.ReadFile(p)
	if err != nil || !looksText(b, false) {
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), searchFileSize)
	for line := 1; sc.Scan(); line++ {
		t := sc.Text()
		if !strings.Contains(strings.ToLower(t), q) {
			continue
		}
		t = strings.TrimSpace(t)
		if len(t) > 200 {
			t = string(trimPartialRune([]byte(t[:200]))) + "…"
		}
		resp.Matches = append(resp.Matches, Match{Path: p, Line: line, Text: t})
		if len(resp.Matches) >= MaxMatches {
			resp.Truncated = true
			return nil
		}
	}
	return nil
}

// write replaces a whole file. The new bytes go to a temporary file first;
// the old file, if any, is kept in VersionsDir; then the new one takes its
// place.
func write(root *os.Root, rel string, data []byte, now time.Time) (Response, error) {
	if len(data) > MaxWrite {
		return Response{}, fmt.Errorf("%w: %d bytes, at most %d per write", ErrTooBig, len(data), MaxWrite)
	}
	if info, err := root.Stat(rel); err == nil && info.IsDir() {
		return Response{}, fmt.Errorf("%s is a folder", rel)
	}
	parent := path.Dir(rel)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return Response{}, err
	}
	tmp := path.Join(parent, ".jaggery-tmp-"+randomHex())
	if err := root.WriteFile(tmp, data, 0o644); err != nil {
		return Response{}, err
	}
	sum := sha256.Sum256(data)
	resp := Response{Path: rel, Written: int64(len(data)), NewSHA256: hex.EncodeToString(sum[:])}
	if _, err := root.Lstat(rel); err == nil {
		resp.OldSHA256 = fileSHA256(root, rel)
		v, err := keepVersion(root, rel, now, true)
		if err != nil {
			_ = root.Remove(tmp)
			return Response{}, err
		}
		resp.Version = v
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return Response{}, err
	}
	return resp, nil
}

// remove moves a file into VersionsDir, where it can be restored for
// KeepVersions.
func remove(root *os.Root, rel string, now time.Time) (Response, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return Response{}, err
	}
	if info.IsDir() {
		return Response{}, fmt.Errorf("%s is a folder: only files can be deleted", rel)
	}
	old := fileSHA256(root, rel)
	v, err := keepVersion(root, rel, now, false)
	if err != nil {
		return Response{}, err
	}
	return Response{Path: rel, Version: v, OldSHA256: old}, nil
}

func fileSHA256(root *os.Root, rel string) string {
	f, err := root.Open(rel)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Version is an old copy of a file, kept by a write or delete.
type Version struct {
	Name string    `json:"name"` // its time stamp, as `jag files restore` takes it
	At   time.Time `json:"at"`
	Size int64     `json:"size"`
}

// Versions lists the old copies of a file in a share, newest first.
func Versions(dir, share, p string) ([]Version, error) {
	s, err := Find(dir, share)
	if err != nil {
		return nil, err
	}
	rel, err := Clean(p)
	if err != nil {
		return nil, err
	}
	root, err := s.Open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(path.Join(VersionsDir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, it := range items {
		at, err := time.Parse(versionStamp, it.Name())
		if err != nil || it.IsDir() {
			continue
		}
		v := Version{Name: it.Name(), At: at}
		if info, err := it.Info(); err == nil {
			v.Size = info.Size()
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Restore puts an old copy of a file back: the newest one when version is
// empty. The file it replaces, if any, is kept as a version itself, so a
// restore can be undone too. Run at the holder's terminal, whatever the
// share's mode.
func Restore(dir, share, p, version string, now time.Time) (Response, error) {
	vs, err := Versions(dir, share, p)
	if err != nil {
		return Response{}, err
	}
	if len(vs) == 0 {
		return Response{}, fmt.Errorf("%s has no old versions in %q", p, share)
	}
	pick := vs[0]
	if version != "" {
		found := false
		for _, v := range vs {
			if v.Name == version {
				pick, found = v, true
				break
			}
		}
		if !found {
			return Response{}, fmt.Errorf("no version %q of %s (jag files versions lists them)", version, p)
		}
	}
	s, _ := Find(dir, share)
	rel, _ := Clean(p)
	root, err := s.Open()
	if err != nil {
		return Response{}, err
	}
	defer root.Close()

	src := path.Join(VersionsDir, rel, pick.Name)
	in, err := root.Open(src)
	if err != nil {
		return Response{}, err
	}
	defer in.Close()
	parent := path.Dir(rel)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return Response{}, err
	}
	tmp := path.Join(parent, ".jaggery-tmp-"+randomHex())
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Response{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = root.Remove(tmp)
		return Response{}, err
	}
	resp := Response{Path: rel, Written: n, NewSHA256: hex.EncodeToString(h.Sum(nil))}
	if _, err := root.Lstat(rel); err == nil {
		resp.OldSHA256 = fileSHA256(root, rel)
		v, err := keepVersion(root, rel, now, true)
		if err != nil {
			_ = root.Remove(tmp)
			return Response{}, err
		}
		resp.Version = v
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return Response{}, err
	}
	return resp, nil
}

// versionStamp names versions so they sort by time.
const versionStamp = "20060102T150405.000000000Z"

// keepVersion puts the current file at rel into VersionsDir/rel/<time>. With
// link, the file stays where it is too (a hard link, so the write that
// follows replaces it atomically); where links aren't supported, or without
// link, the file is moved. Versions of that file older than KeepVersions are
// dropped.
func keepVersion(root *os.Root, rel string, now time.Time, link bool) (string, error) {
	vdir := path.Join(VersionsDir, rel)
	if err := root.MkdirAll(vdir, 0o700); err != nil {
		return "", err
	}
	v := path.Join(vdir, now.UTC().Format(versionStamp))
	if link && root.Link(rel, v) == nil {
		pruneVersions(root, vdir, now)
		return v, nil
	}
	if err := root.Rename(rel, v); err != nil {
		return "", err
	}
	pruneVersions(root, vdir, now)
	return v, nil
}

func pruneVersions(root *os.Root, vdir string, now time.Time) {
	f, err := root.Open(vdir)
	if err != nil {
		return
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	for _, n := range names {
		if t, err := time.Parse(versionStamp, n); err == nil && now.Sub(t) > KeepVersions {
			_ = root.Remove(path.Join(vdir, n))
		}
	}
}

func randomHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
