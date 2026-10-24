package pairing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	inviteFile = "invite.json"

	// InviteTTL is how long a code works: long enough to walk to the other
	// machine, short enough that a leaked code is soon useless.
	InviteTTL = 10 * time.Minute

	// MaxFailures wrong codes burn the invite, so it can't be guessed online.
	MaxFailures = 5
)

// Invite is a pending join code. `jag invite` writes it; the agent checks
// joins against it and deletes it once used, expired or guessed at too often.
type Invite struct {
	Code     string    `json:"code"`
	Expires  time.Time `json:"expires"`
	Failures int       `json:"failures"`
}

var ErrNoInvite = errors.New("no invite pending: run `jag invite` on a machine already in the house")

// CreateInvite replaces any pending invite with a fresh code.
func CreateInvite(dir string, now time.Time) (Invite, error) {
	code, err := NewCode()
	if err != nil {
		return Invite{}, err
	}
	inv := Invite{Code: code, Expires: now.Add(InviteTTL)}
	return inv, saveInvite(dir, inv)
}

// LoadInvite returns the pending invite, treating an expired one as none.
func LoadInvite(dir string, now time.Time) (Invite, error) {
	raw, err := os.ReadFile(filepath.Join(dir, inviteFile))
	if errors.Is(err, os.ErrNotExist) {
		return Invite{}, ErrNoInvite
	}
	if err != nil {
		return Invite{}, fmt.Errorf("reading invite: %w", err)
	}
	var inv Invite
	if err := json.Unmarshal(raw, &inv); err != nil {
		return Invite{}, fmt.Errorf("parsing invite: %w", err)
	}
	if !now.Before(inv.Expires) {
		_ = DeleteInvite(dir)
		return Invite{}, ErrNoInvite
	}
	return inv, nil
}

// RecordFailure counts a wrong code, and burns the invite at MaxFailures.
func RecordFailure(dir string, inv Invite) error {
	inv.Failures++
	if inv.Failures >= MaxFailures {
		return DeleteInvite(dir)
	}
	return saveInvite(dir, inv)
}

func DeleteInvite(dir string) error {
	err := os.Remove(filepath.Join(dir, inviteFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func saveInvite(dir string, inv Invite) error {
	raw, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, inviteFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("writing invite: %w", err)
	}
	return os.Rename(tmp, path)
}
