package pairing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

func TestWordlistIsAFullByte(t *testing.T) {
	if len(wordlist) != 256 {
		t.Fatalf("wordlist has %d words, want 256 (one per byte value)", len(wordlist))
	}
	seen := map[string]bool{}
	for _, w := range wordlist {
		if seen[w] {
			t.Fatalf("duplicate word %q", w)
		}
		seen[w] = true
	}
}

func TestParseCodeNormalisesWhatPeopleType(t *testing.T) {
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseCode(code); err != nil || got != code {
		t.Fatalf("own code: got %q, %v", got, err)
	}
	if got, err := ParseCode("Maple River otter-candle QUARTZ fig"); err != nil || got != "maple-river-otter-candle-quartz-fig" {
		t.Fatalf("mixed input: got %q, %v", got, err)
	}
	for _, bad := range []string{"192.0.2.55", "maple-river", "maple-river-otter-candle-quartz-banana"} {
		if _, err := ParseCode(bad); !errors.Is(err, ErrNotACode) {
			t.Errorf("%q: got %v, want ErrNotACode", bad, err)
		}
	}
}

type machine struct {
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newMachine(t *testing.T) machine {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return machine{id: identity.IDFromKey(pub), pub: pub, priv: priv}
}

func (m machine) identity() *identity.Identity {
	return &identity.Identity{ID: m.id, Name: "m-" + m.id[:4], PublicKey: m.pub, PrivateKey: m.priv}
}

// exchange runs both sides, as the joiner and inviter would.
func exchange(t *testing.T, joinerCode, inviterCode string, joiner, inviter machine) (Verified, error) {
	t.Helper()
	req, nonce, err := NewJoinRequest(joinerCode, joiner.id, "joiner", joiner.pub, 7373)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckJoinRequest(inviterCode, req); err != nil {
		return Verified{}, err
	}
	att, err := Answer(inviter.identity(), Challenge{Nonce: req.Nonce})
	if err != nil {
		t.Fatal(err)
	}
	return CheckJoinResponse(joinerCode, req, nonce, Accept(inviterCode, att, req))
}

func TestJoinSucceedsWhenBothKnowTheCode(t *testing.T) {
	joiner, inviter := newMachine(t), newMachine(t)
	code, _ := NewCode()
	v, err := exchange(t, code, code, joiner, inviter)
	if err != nil {
		t.Fatal(err)
	}
	if v.MachineID != inviter.id {
		t.Fatalf("verified %q, want the inviter %q", v.MachineID, inviter.id)
	}
}

func TestJoinFailsOnAWrongCode(t *testing.T) {
	joiner, inviter := newMachine(t), newMachine(t)
	a, _ := NewCode()
	b, _ := NewCode()
	if _, err := exchange(t, a, b, joiner, inviter); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("got %v, want ErrWrongCode", err)
	}
}

// A machine that intercepts the request can't swap in its own key: the MAC
// binds the joiner's key, and the attacker doesn't know the code.
func TestJoinRejectsASubstitutedKey(t *testing.T) {
	joiner, attacker := newMachine(t), newMachine(t)
	code, _ := NewCode()
	req, _, err := NewJoinRequest(code, joiner.id, "joiner", joiner.pub, 7373)
	if err != nil {
		t.Fatal(err)
	}
	req.MachineID = attacker.id
	req.PublicKey = base64.StdEncoding.EncodeToString(attacker.pub)
	if _, err := CheckJoinRequest(code, req); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("got %v, want ErrWrongCode", err)
	}
}

// An impersonator that doesn't know the code can't pass as the inviter, even
// with a valid signature from its own key.
func TestJoinerRejectsAnInviterWithoutTheCode(t *testing.T) {
	joiner, impostor := newMachine(t), newMachine(t)
	code, _ := NewCode()
	guess, _ := NewCode()
	req, nonce, _ := NewJoinRequest(code, joiner.id, "joiner", joiner.pub, 7373)
	att, _ := Answer(impostor.identity(), Challenge{Nonce: req.Nonce})
	if _, err := CheckJoinResponse(code, req, nonce, Accept(guess, att, req)); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("got %v, want ErrWrongCode", err)
	}
}

func TestInviteLifecycle(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	if _, err := LoadInvite(dir, now); !errors.Is(err, ErrNoInvite) {
		t.Fatalf("empty dir: got %v", err)
	}
	inv, err := CreateInvite(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := LoadInvite(dir, now); err != nil || got.Code != inv.Code {
		t.Fatalf("load: %v %v", got, err)
	}
	if _, err := LoadInvite(dir, now.Add(InviteTTL)); !errors.Is(err, ErrNoInvite) {
		t.Fatalf("expired: got %v, want ErrNoInvite", err)
	}

	inv, _ = CreateInvite(dir, now)
	for i := 0; i < MaxFailures; i++ {
		cur, err := LoadInvite(dir, now)
		if err != nil {
			t.Fatalf("burned after %d failures, want %d", i, MaxFailures)
		}
		_ = RecordFailure(dir, cur)
	}
	if _, err := LoadInvite(dir, now); !errors.Is(err, ErrNoInvite) {
		t.Fatalf("after %d failures: got %v, want burned", MaxFailures, err)
	}
}
