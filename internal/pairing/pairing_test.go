package pairing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

func testIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	t.Setenv("JAGGERY_STATE_DIR", t.TempDir())
	id, _, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return id
}

func TestAnswerVerifiesAgainstChallenge(t *testing.T) {
	id := testIdentity(t)

	challenge, nonce, err := NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	att, err := Answer(id, challenge)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}

	verified, err := Verify(nonce, att)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.MachineID != id.ID {
		t.Errorf("machine id = %q, want %q", verified.MachineID, id.ID)
	}
	if verified.Fingerprint != id.Fingerprint() {
		t.Errorf("fingerprint = %q, want %q", verified.Fingerprint, id.Fingerprint())
	}
}

// A signature over a different nonce must not verify — this is what stops a
// captured attestation being replayed in a later exchange.
func TestVerifyRejectsReplayedSignature(t *testing.T) {
	id := testIdentity(t)
	first, firstNonce, _ := NewChallenge()
	att, _ := Answer(id, first)

	_, freshNonce, _ := NewChallenge()
	if _, err := Verify(freshNonce, att); err != ErrBadSignature {
		t.Errorf("want ErrBadSignature for a replay, got %v", err)
	}

	// Sanity: it does verify against its own nonce.
	if _, err := Verify(firstNonce, att); err != nil {
		t.Errorf("attestation should verify against its own nonce: %v", err)
	}
}

// A machine cannot claim an id it does not own: swapping in another key's id
// while signing with its own key must be caught.
func TestVerifyRejectsIDMismatch(t *testing.T) {
	id := testIdentity(t)
	challenge, nonce, _ := NewChallenge()
	att, _ := Answer(id, challenge)

	att.MachineID = "deadbeefcafe" // claim someone else's id

	if _, err := Verify(nonce, att); err != ErrIDMismatch {
		t.Errorf("want ErrIDMismatch, got %v", err)
	}
}

// An impostor who signs the nonce with their own key but presents the victim's
// public key fails signature verification.
func TestVerifyRejectsForeignKeySignature(t *testing.T) {
	victim := testIdentity(t)
	challenge, nonce, _ := NewChallenge()

	_, impostorPriv, _ := ed25519.GenerateKey(rand.Reader)
	nonceBytes, _ := base64.StdEncoding.DecodeString(challenge.Nonce)
	forged := Attestation{
		MachineID: victim.ID,
		Name:      victim.Name,
		PublicKey: base64.StdEncoding.EncodeToString(victim.PublicKey),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(impostorPriv, nonceBytes)),
	}

	if _, err := Verify(nonce, forged); err != ErrBadSignature {
		t.Errorf("want ErrBadSignature, got %v", err)
	}
}

func TestVerifyRejectsMalformedKey(t *testing.T) {
	att := Attestation{
		MachineID: "abc",
		PublicKey: base64.StdEncoding.EncodeToString([]byte("too short")),
		Signature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	}
	if _, err := Verify(make([]byte, nonceLen), att); err == nil {
		t.Error("want an error for a wrong-sized key, got nil")
	}
}
