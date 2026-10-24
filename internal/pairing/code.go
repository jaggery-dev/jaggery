package pairing

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

// A join code lets a new machine join the house with no address typed: the
// person runs `jag invite` on a machine already in the house, and
// `jag join <code>` on the new one, which finds the house over mDNS.
//
// The code itself never crosses the network. Each side proves it knows the
// code with an HMAC keyed by it and bound to its own public key, so a machine
// that intercepts or impersonates one side learns nothing it can replay, and
// can't substitute its own key. 48 bits, a 10-minute life and a cap of 5 wrong
// attempts make guessing it online hopeless; an impersonator brute-forcing a
// captured MAC offline needs ~2^47 HMACs, hours of GPU time, while the invite
// expires in minutes.
const (
	CodeWords = 6
	JoinPath  = "/v1/pair/join"
)

// NewCode returns a fresh code like "maple-river-otter-candle-quartz-fig".
func NewCode() (string, error) {
	raw := make([]byte, CodeWords)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating code: %w", err)
	}
	words := make([]string, CodeWords)
	for i, b := range raw {
		words[i] = wordlist[b]
	}
	return strings.Join(words, "-"), nil
}

var ErrNotACode = errors.New("not a join code")

// ParseCode normalises what a person typed: any case, separated by hyphens or
// spaces. It fails unless every word is from the list.
func ParseCode(s string) (string, error) {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == '-' || r == ' ' })
	if len(fields) != CodeWords {
		return "", ErrNotACode
	}
	for _, f := range fields {
		if !isWord(f) {
			return "", fmt.Errorf("%w: %q is not a code word", ErrNotACode, f)
		}
	}
	return strings.Join(fields, "-"), nil
}

func isWord(w string) bool {
	for _, x := range wordlist {
		if x == w {
			return true
		}
	}
	return false
}

// JoinRequest is what the joining machine sends to each agent it finds.
type JoinRequest struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"` // base64, raw ed25519
	Port      int    `json:"port"`       // the joiner's agent port, for the inviter's store
	Nonce     string `json:"nonce"`      // base64; the inviter signs it
	MAC       string `json:"mac"`        // base64; proves the joiner knows the code
}

// JoinResponse is the inviter's answer: proof it holds its key (a signature
// over the joiner's nonce) and proof it knows the code, bound to both keys.
type JoinResponse struct {
	Attestation Attestation `json:"attestation"`
	MAC         string      `json:"mac"`
}

func codeKey(code string) []byte {
	sum := sha256.Sum256([]byte("jaggery-join-v1\x00" + code))
	return sum[:]
}

func mac(code string, parts ...string) []byte {
	h := hmac.New(sha256.New, codeKey(code))
	for _, p := range parts {
		// Length-prefix each part so no two different part lists hash alike.
		fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return h.Sum(nil)
}

// NewJoinRequest builds the joiner's request, and returns the nonce to check
// the inviter's answer against.
func NewJoinRequest(code, machineID, name string, pub ed25519.PublicKey, port int) (JoinRequest, []byte, error) {
	challenge, nonce, err := NewChallenge()
	if err != nil {
		return JoinRequest{}, nil, err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	req := JoinRequest{
		MachineID: machineID,
		Name:      name,
		PublicKey: pubB64,
		Port:      port,
		Nonce:     challenge.Nonce,
	}
	req.MAC = base64.StdEncoding.EncodeToString(mac(code, "join", machineID, name, pubB64, challenge.Nonce))
	return req, nonce, nil
}

var (
	ErrWrongCode = errors.New("the code does not match")
	ErrBadJoiner = errors.New("joining machine's key does not match the id it claims")
)

// CheckJoinRequest is the inviter's side: does the joiner know the code, and
// does its key derive the id it claims? It returns the joiner's key.
func CheckJoinRequest(code string, req JoinRequest) (ed25519.PublicKey, error) {
	got, err := base64.StdEncoding.DecodeString(req.MAC)
	if err != nil {
		return nil, ErrWrongCode
	}
	want := mac(code, "join", req.MachineID, req.Name, req.PublicKey, req.Nonce)
	if !hmac.Equal(got, want) {
		return nil, ErrWrongCode
	}
	pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, ErrBadJoiner
	}
	if identity.IDFromKey(pub) != req.MachineID {
		return nil, ErrBadJoiner
	}
	return ed25519.PublicKey(pub), nil
}

// Accept builds the inviter's answer to a checked request.
func Accept(code string, att Attestation, req JoinRequest) JoinResponse {
	return JoinResponse{
		Attestation: att,
		MAC:         base64.StdEncoding.EncodeToString(mac(code, "accept", att.MachineID, att.PublicKey, req.PublicKey, req.Nonce)),
	}
}

// CheckJoinResponse is the joiner's side: the inviter must hold its key and
// know the code. Only then is it the machine the person invited from.
func CheckJoinResponse(code string, req JoinRequest, nonce []byte, resp JoinResponse) (Verified, error) {
	got, err := base64.StdEncoding.DecodeString(resp.MAC)
	if err != nil {
		return Verified{}, ErrWrongCode
	}
	want := mac(code, "accept", resp.Attestation.MachineID, resp.Attestation.PublicKey, req.PublicKey, req.Nonce)
	if !hmac.Equal(got, want) {
		return Verified{}, ErrWrongCode
	}
	return Verify(nonce, resp.Attestation)
}
