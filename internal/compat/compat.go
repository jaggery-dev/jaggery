// Package compat holds the contracts that let machines on different builds
// share a house (tech-specs/releases.md): the house protocol
// spoken between agents, and the schema of the files in the state directory.
// Both are versioned apart from the release, because a house always has
// machines on different builds during a rollout.
package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The house protocol: the mutual-TLS APIs agents call on each other.
// Protocol is what this build speaks; it still works with peers down to
// MinProtocol. Bump Protocol for a breaking change, and raise MinProtocol
// only once no supported release speaks the older one.
const (
	Protocol    = 1
	MinProtocol = 1
)

// Compatible reports whether this build can work with a peer that speaks
// protocols min..max: the two ranges must overlap. Builds from before the
// protocol was reported (zeros) speak 1..1.
func Compatible(min, max int) bool {
	if max == 0 {
		min, max = 1, 1
	}
	if min == 0 {
		min = max
	}
	lo, hi := MinProtocol, Protocol
	if min > lo {
		lo = min
	}
	if max < hi {
		hi = max
	}
	return lo <= hi
}

// CLIOutput is the version of the envelope `jag <command> --json` prints
// (docs/reference/json.md). Adding a field is not a change; renaming or
// removing one, or changing its meaning, bumps it.
const CLIOutput = 1

// The state directory's schema. StateSchema is what this build writes.
// MinReader in the file says which schema a binary must understand to read
// it; a migration that older builds can't read raises it, and must back the
// state up first so a rollback can be undone by hand.
const StateSchema = 1

const stateFile = "state.json"

type state struct {
	Schema    int `json:"schema"`
	MinReader int `json:"min_reader"`
}

// Migration moves the state directory from schema From to From+1.
type Migration struct {
	From int
	Run  func(dir string) error
	// Breaking marks a migration older builds can't read past.
	Breaking bool
}

// Migrations is the ordered list; none yet.
var Migrations []Migration

var ErrNewerState = errors.New("this machine's state was written by a newer Jaggery")

// PrepareState checks and migrates the state directory before the agent
// uses it: a new directory is stamped with the current schema; an older one
// is backed up and migrated forward; one this build can't read is refused.
func PrepareState(dir string, now time.Time) error {
	path := filepath.Join(dir, stateFile)
	var st state
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		st = state{Schema: 1, MinReader: 1} // every state directory before P0d
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(raw, &st); err != nil {
			return fmt.Errorf("reading %s: %w", stateFile, err)
		}
	}
	if st.MinReader > StateSchema {
		return fmt.Errorf("%w (schema %d, readable from %d; this build reads %d): update this build with `jag update`, or restore a backup from %s",
			ErrNewerState, st.Schema, st.MinReader, StateSchema, filepath.Join(dir, "backups"))
	}
	if st.Schema < StateSchema {
		if err := backup(dir, st.Schema, now); err != nil {
			return fmt.Errorf("backing up state before migrating: %w", err)
		}
		for _, m := range Migrations {
			if m.From < st.Schema || m.From >= StateSchema {
				continue
			}
			if err := m.Run(dir); err != nil {
				return fmt.Errorf("migrating state from schema %d: %w", m.From, err)
			}
			st.Schema = m.From + 1
			if m.Breaking {
				st.MinReader = st.Schema
			}
		}
	}
	if st.Schema < StateSchema {
		st.Schema = StateSchema
	}
	out, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if string(out) == string(raw) {
		return nil
	}
	if err := os.WriteFile(path+".tmp", out, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// backup copies the state files (not the backups or caches) aside.
func backup(dir string, schema int, now time.Time) error {
	dst := filepath.Join(dir, "backups", fmt.Sprintf("schema-%d-%s", schema, now.UTC().Format("20060102-150405")))
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) == ".lock" {
			continue
		}
		if err := copyFile(filepath.Join(dir, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
