package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Model approval: the router serves only models a person has
// approved, pinned to the exact files (the digest), so a model swapped in
// under a known name is refused too. Approvals are house-wide: agents share
// them over mutual TLS, so approving once, anywhere, approves everywhere.
//
// Runtimes that report no digest (LM Studio, llama.cpp) are approved by name
// alone; the pin is only as strong as what the runtime tells us.

const (
	approvalsFile = "approvals.json"
	approvalsLock = "approvals.lock"
)

// Approval lets a model through the router.
type Approval struct {
	Name   string    `json:"name"`
	Digest string    `json:"digest,omitempty"`
	At     time.Time `json:"at"`
	// By says who approved it: a machine's name, or "baseline" for the
	// models a machine already had when approval began.
	By string `json:"by"`
}

type approvals struct {
	// Enforce turns approval on. A house that hasn't started (no file) is
	// started by the agent with its current models as the baseline.
	Enforce  bool       `json:"enforce"`
	Approved []Approval `json:"approved"`
}

// Approvals is the on-disk approval list, shared by the agent and the CLI.
type Approvals struct {
	dir string
}

func OpenApprovals(dir string) *Approvals { return &Approvals{dir: dir} }

var ErrNoApprovals = errors.New("model approval hasn't started on this machine yet")

// Started reports whether this machine has an approval list.
func (a *Approvals) Started() bool {
	_, err := os.Stat(filepath.Join(a.dir, approvalsFile))
	return err == nil
}

// Start creates the list, approving the given models as the baseline. A
// no-op if it already exists.
func (a *Approvals) Start(baseline []Approval) error {
	return a.mutate(func(d *approvals, exists bool) (bool, error) {
		if exists {
			return false, nil
		}
		d.Enforce = true
		d.Approved = dedupe(baseline)
		return true, nil
	})
}

// Enforcing reports whether unapproved models are refused. Fails open (not
// enforcing) only when approval hasn't started.
func (a *Approvals) Enforcing() bool {
	d, ok := a.read()
	return ok && d.Enforce
}

func (a *Approvals) SetEnforce(on bool) error {
	return a.mutate(func(d *approvals, exists bool) (bool, error) {
		if !exists {
			return false, ErrNoApprovals
		}
		d.Enforce = on
		return true, nil
	})
}

// Approved reports whether this exact model may be served. With approval
// off or not started, everything is.
func (a *Approvals) Approved(name, digest string) bool {
	d, ok := a.read()
	if !ok || !d.Enforce {
		return true
	}
	for _, ap := range d.Approved {
		if ap.Name == name && ap.Digest == digest {
			return true
		}
	}
	return false
}

// Add approves models, returning how many were new. Used by `jag models
// approve` and by merging other machines' lists.
func (a *Approvals) Add(list ...Approval) (int, error) {
	added := 0
	err := a.mutate(func(d *approvals, exists bool) (bool, error) {
		if !exists {
			return false, ErrNoApprovals
		}
		for _, ap := range list {
			if !contains(d.Approved, ap) {
				d.Approved = append(d.Approved, ap)
				added++
			}
		}
		return added > 0, nil
	})
	return added, err
}

// All returns the approval list.
func (a *Approvals) All() []Approval {
	d, _ := a.read()
	return d.Approved
}

func contains(list []Approval, ap Approval) bool {
	for _, x := range list {
		if x.Name == ap.Name && x.Digest == ap.Digest {
			return true
		}
	}
	return false
}

func dedupe(list []Approval) []Approval {
	out := []Approval{}
	for _, ap := range list {
		if !contains(out, ap) {
			out = append(out, ap)
		}
	}
	return out
}

func (a *Approvals) read() (approvals, bool) {
	raw, err := os.ReadFile(filepath.Join(a.dir, approvalsFile))
	if err != nil {
		return approvals{}, false
	}
	var d approvals
	if json.Unmarshal(raw, &d) != nil {
		return approvals{}, false
	}
	return d, true
}

// mutate runs fn under the cross-process lock and saves if it says so.
func (a *Approvals) mutate(fn func(d *approvals, exists bool) (bool, error)) error {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(a.dir, approvalsLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock: %w", err)
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("locking approvals: %w", err)
	}
	defer unlockFile(lock)

	d, exists := a.read()
	save, err := fn(&d, exists)
	if err != nil || !save {
		return err
	}
	if d.Approved == nil {
		d.Approved = []Approval{}
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(a.dir, approvalsFile)
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
