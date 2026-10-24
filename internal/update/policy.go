package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Policy is what the agent does when a newer release appears.
type Policy string

const (
	Auto   Policy = "auto"   // install it, staggered across the house
	Notify Policy = "notify" // say so in jag status and on the dashboard
	Off    Policy = "off"    // don't check
)

// Settings are this machine's update choices, in update.json.
type Settings struct {
	Policy  Policy `json:"policy"`
	Channel string `json:"channel"`
}

// DefaultSettings follow the build: beta builds update themselves (the
// author's house), prod builds only say an update exists (testers), and a dev
// build, being tested by hand, is never replaced behind its author's back.
func DefaultSettings(buildChannel string) Settings {
	switch buildChannel {
	case "beta":
		return Settings{Policy: Auto, Channel: "beta"}
	case "prod":
		return Settings{Policy: Notify, Channel: "prod"}
	}
	return Settings{Policy: Notify, Channel: "beta"}
}

func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(s); p {
	case Auto, Notify, Off:
		return p, nil
	}
	return "", fmt.Errorf("policy must be auto, notify or off, not %q", s)
}

func LoadSettings(dir, buildChannel string) Settings {
	s := DefaultSettings(buildChannel)
	if raw, err := os.ReadFile(filepath.Join(dir, "update.json")); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	if _, err := ParsePolicy(string(s.Policy)); err != nil {
		s.Policy = DefaultSettings(buildChannel).Policy
	}
	if s.Channel != "prod" {
		s.Channel = "beta"
	}
	return s
}

func SaveSettings(dir string, s Settings) error {
	return writeJSON(filepath.Join(dir, "update.json"), s)
}

// Status is what the agent last learned about updates, shown by jag status
// and the dashboard.
type Status struct {
	Policy    Policy    `json:"policy"`
	Channel   string    `json:"channel"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	// Available is the newer release, if there is one.
	Available string `json:"available,omitempty"`
	// Via names the peer relaying it, when GitHub couldn't be reached.
	Via string `json:"via,omitempty"`
	// Waiting says why an auto update hasn't happened yet.
	Waiting string `json:"waiting,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Pending marks a restart into a new build that hasn't proven itself yet. A
// build that keeps crashing on start is rolled back after MaxAttempts.
type Pending struct {
	From     string    `json:"from"` // the commit we updated from
	At       time.Time `json:"at"`
	Attempts int       `json:"attempts"`
}

const (
	pendingFile = "update-pending.json"
	badFile     = "update-bad.json"
	MaxAttempts = 3
)

func SavePending(dir string, p Pending) error { return writeJSON(filepath.Join(dir, pendingFile), p) }

func LoadPending(dir string) (Pending, bool) {
	var p Pending
	raw, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return Pending{}, false
	}
	return p, true
}

func ClearPending(dir string) {
	if err := os.Remove(filepath.Join(dir, pendingFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "warning: clearing the update marker:", err)
	}
}

// Withdrawn lists the commits of releases this machine saw withdrawn on
// GitHub. Shared in inventory: every machine skips them, and one running
// such a build moves off it.
func Withdrawn(dir string) []string {
	var list []string
	if raw, err := os.ReadFile(filepath.Join(dir, "update-withdrawn.json")); err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}

func MarkWithdrawn(dir string, commits ...string) error {
	list := Withdrawn(dir)
	changed := false
	for _, c := range commits {
		known := false
		for _, w := range list {
			known = known || w == c
		}
		if !known && c != "" {
			list = append(list, c)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeJSON(filepath.Join(dir, "update-withdrawn.json"), list)
}

// Bad lists commits this machine rolled back from. The agent shares them in
// its inventory so the rest of the house skips those builds too.
func Bad(dir string) []string {
	var bad []string
	if raw, err := os.ReadFile(filepath.Join(dir, badFile)); err == nil {
		_ = json.Unmarshal(raw, &bad)
	}
	return bad
}

func MarkBad(dir, commit string) error {
	bad := Bad(dir)
	for _, c := range bad {
		if c == commit {
			return nil
		}
	}
	return writeJSON(filepath.Join(dir, badFile), append(bad, commit))
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
