// Package pairing defines the proof-of-possession exchange that lets one
// machine verify another's identity before trusting it.
//
// The problem it solves: discovery advertises a machine id in an mDNS TXT
// record, but anyone on the network can put any id there. Before we trust a
// machine we make it prove it holds the private key behind the id it claims.
// The verifier sends a fresh nonce; the responder signs it; the verifier
// checks the signature against the responder's public key and checks that the
// key actually derives the claimed id. A fresh nonce each time stops a replay.
//
// This is trust-on-first-use: the first exchange is still only as trustworthy
// as the channel it ran over. mTLS pins the key for every exchange after.
package pairing

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

// ProvePath is where an agent answers the proof-of-possession exchange.
const ProvePath = "/v1/identity/prove"

// Challenge is sent by the verifier.
type Challenge struct {
	Nonce string `json:"nonce"` // base64, raw
}

// Attestation is the responder's signed answer.
type Attestation struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"` // base64, raw ed25519 key
	Signature string `json:"signature"`  // base64, signature over the nonce
}

const nonceLen = 32

func NewChallenge() (Challenge, []byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return Challenge{}, nil, fmt.Errorf("generating nonce: %w", err)
	}
	return Challenge{Nonce: base64.StdEncoding.EncodeToString(nonce)}, nonce, nil
}

// Answer signs the challenge with this machine's key.
func Answer(id *identity.Identity, c Challenge) (Attestation, error) {
	nonce, err := base64.StdEncoding.DecodeString(c.Nonce)
	if err != nil {
		return Attestation{}, fmt.Errorf("decoding nonce: %w", err)
	}
	// Anyone on the LAN can ask for this signature, so sign nothing but a
	// nonce of exactly this length. Every other message this key signs (the
	// house roster) is longer, so a stranger can't get one signed here.
	if len(nonce) != nonceLen {
		return Attestation{}, fmt.Errorf("challenge nonce must be %d bytes", nonceLen)
	}
	sig := ed25519.Sign(id.PrivateKey, nonce)
	return Attestation{
		MachineID: id.ID,
		Name:      id.Name,
		PublicKey: base64.StdEncoding.EncodeToString(id.PublicKey),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}, nil
}

// Fetch runs the exchange against a peer: challenge it, read its signed
// answer, and verify it. The caller supplies the HTTP client so the mTLS path
// can pass a pinned-key transport later; a plain client is fine for first
// contact. baseURL is like "http://192.0.2.73:7373".
func Fetch(ctx context.Context, client *http.Client, baseURL string) (Verified, error) {
	challenge, nonce, err := NewChallenge()
	if err != nil {
		return Verified{}, err
	}
	body, err := json.Marshal(challenge)
	if err != nil {
		return Verified{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+ProvePath, bytes.NewReader(body))
	if err != nil {
		return Verified{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return Verified{}, fmt.Errorf("reaching %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Verified{}, fmt.Errorf("%s answered %s", baseURL, resp.Status)
	}

	var att Attestation
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&att); err != nil {
		return Verified{}, fmt.Errorf("reading response: %w", err)
	}
	return Verify(nonce, att)
}

// Verified is a peer whose key has been proven against its claimed id.
type Verified struct {
	MachineID   string
	Name        string
	PublicKey   ed25519.PublicKey
	Fingerprint string
}

var (
	ErrBadSignature = errors.New("peer did not prove it holds its key")
	ErrIDMismatch   = errors.New("peer's key does not match the id it claims")
)

// Verify checks that the attestation was signed by the key it carries, over
// the nonce we sent, and that the key derives the id the peer claims.
func Verify(nonce []byte, att Attestation) (Verified, error) {
	pub, err := base64.StdEncoding.DecodeString(att.PublicKey)
	if err != nil {
		return Verified{}, fmt.Errorf("decoding public key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Verified{}, fmt.Errorf("public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	sig, err := base64.StdEncoding.DecodeString(att.Signature)
	if err != nil {
		return Verified{}, fmt.Errorf("decoding signature: %w", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(pub), nonce, sig) {
		return Verified{}, ErrBadSignature
	}
	if identity.IDFromKey(pub) != att.MachineID {
		return Verified{}, ErrIDMismatch
	}

	return Verified{
		MachineID:   att.MachineID,
		Name:        att.Name,
		PublicKey:   ed25519.PublicKey(pub),
		Fingerprint: identity.FingerprintFromKey(pub),
	}, nil
}
