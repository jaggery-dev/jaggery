// Package shares holds the folders this machine lends to the house's models
// and agents (tech-specs/shared-folders.md). A share is a folder
// on the machine that holds it; files stay where they are and are opened
// through os.Root, so a path can't climb out of the share, not even through a
// symlink.
//
// Shares are added only at this machine's terminal (`jag share add`), never
// over the network, so one machine can't publish another machine's folders.
// The agent runs as a user, and can serve only what that user can read.
package shares

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// File is the shares' file in the state directory.
const File = "shares.json"

// VersionsDir keeps old versions of files changed in a writable share (S2);
// it is excluded like any other dot-folder.
const VersionsDir = ".jaggery-versions"

// Share is one folder lent to the house.
type Share struct {
	// Name is how models and agents refer to it: "docs", "photos".
	Name string `json:"name"`
	// Path is the folder's absolute path on this machine. It never leaves
	// the machine: the house sees only the name.
	Path     string    `json:"path"`
	Writable bool      `json:"writable,omitempty"`
	AddedAt  time.Time `json:"added_at"`
}

// Info is what the rest of the house learns about a share: its name and
// whether it can be written, not where it lives.
type Info struct {
	Name     string `json:"name"`
	Writable bool   `json:"writable,omitempty"`
	// Use is today's calls on it (S3): counts and app names, no paths.
	Use *Use `json:"use,omitempty"`
}

type file struct {
	Shares []Share `json:"shares"`
}

var (
	// ErrNotFound: no share by that name.
	ErrNotFound = errors.New("no such share")
	// ErrExcluded: the path is one shares never serve (dotfiles, keys).
	ErrExcluded = errors.New("excluded from shares")
	// ErrBadPath: the path isn't a clean path inside the share.
	ErrBadPath = errors.New("not a path inside the share")

	validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

// Load reads this machine's shares, sorted by name; none when the file is
// missing.
func Load(dir string) ([]Share, error) {
	b, err := os.ReadFile(filepath.Join(dir, File))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", File, err)
	}
	sort.Slice(f.Shares, func(i, j int) bool { return f.Shares[i].Name < f.Shares[j].Name })
	return f.Shares, nil
}

// Infos is Load reduced to what the house may know, with today's use; nil
// on any error, so a broken file lends nothing.
func Infos(dir string, now time.Time) []Info {
	list, err := Load(dir)
	if err != nil {
		return nil
	}
	use := UseToday(dir, now)
	out := make([]Info, 0, len(list))
	for _, s := range list {
		out = append(out, Info{Name: s.Name, Writable: s.Writable, Use: use[s.Name]})
	}
	return out
}

func save(dir string, list []Share) error {
	b, err := json.MarshalIndent(file{Shares: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, File+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, File))
}

// Add lends a folder to the house. The name defaults to the folder's base
// name, lower-cased.
func Add(dir, folder, name string, writable bool, now time.Time) (Share, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return Share{}, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return Share{}, fmt.Errorf("%s: %w", folder, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Share{}, err
	}
	if !st.IsDir() {
		return Share{}, fmt.Errorf("%s is not a folder", abs)
	}
	if abs == filepath.Dir(abs) {
		return Share{}, errors.New("refusing to share the whole filesystem")
	}
	if home, err := os.UserHomeDir(); err == nil && abs == filepath.Clean(home) {
		return Share{}, errors.New("refusing to share the whole home folder: pick the folders inside it")
	}
	state, err := filepath.EvalSymlinks(dir)
	if err != nil {
		state = dir
	}
	if within(state, abs) || within(abs, state) {
		return Share{}, errors.New("refusing to share Jaggery's state folder (it holds this machine's keys)")
	}
	if name == "" {
		name = strings.ToLower(filepath.Base(abs))
	}
	if !validName.MatchString(name) {
		return Share{}, fmt.Errorf("share name %q: use lower-case letters, digits, - and _ (up to 32), or pass --name", name)
	}

	list, err := Load(dir)
	if err != nil {
		return Share{}, err
	}
	for _, s := range list {
		if s.Name == name {
			return Share{}, fmt.Errorf("a share named %q already exists (%s)", name, s.Path)
		}
		if within(s.Path, abs) || within(abs, s.Path) {
			return Share{}, fmt.Errorf("%s overlaps the share %q (%s)", abs, s.Name, s.Path)
		}
	}
	s := Share{Name: name, Path: abs, Writable: writable, AddedAt: now.UTC()}
	if err := save(dir, append(list, s)); err != nil {
		return Share{}, err
	}
	return s, nil
}

// Remove stops lending a share. The folder itself is untouched.
func Remove(dir, name string) error {
	list, err := Load(dir)
	if err != nil {
		return err
	}
	for i, s := range list {
		if s.Name == name {
			return save(dir, append(list[:i], list[i+1:]...))
		}
	}
	return fmt.Errorf("%w %q", ErrNotFound, name)
}

// Find returns the share by name.
func Find(dir, name string) (Share, error) {
	list, err := Load(dir)
	if err != nil {
		return Share{}, err
	}
	for _, s := range list {
		if s.Name == name {
			return s, nil
		}
	}
	return Share{}, fmt.Errorf("%w %q", ErrNotFound, name)
}

// within reports whether p is base or inside it.
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Clean turns a path an agent sent ("notes/a.md", "/notes/a.md", "") into a
// slash-separated path relative to the share's root, "." for the root. It
// refuses paths that leave the share and paths shares never serve.
func Clean(p string) (string, error) {
	if strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return "", ErrBadPath
	}
	c := path.Clean("/" + p)
	if c == "/" {
		return ".", nil
	}
	c = strings.TrimPrefix(c, "/")
	if Excluded(c) {
		return "", fmt.Errorf("%s: %w", c, ErrExcluded)
	}
	return c, nil
}

// Excluded reports whether a share-relative, slash-separated path is one
// shares never serve: anything under a dotfile or dot-folder (.git, .ssh,
// .env, the versions folder) and private key files.
func Excluded(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") && part != "." {
			return true
		}
	}
	base := strings.ToLower(path.Base(rel))
	return strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem")
}

// Open opens the share's folder as a root; every file access goes through
// it, so symlinks leading outside the share are refused by the OS layer.
func (s Share) Open() (*os.Root, error) {
	return os.OpenRoot(s.Path)
}
