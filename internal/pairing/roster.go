package pairing

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

// A roster is a machine's signed list of the machines it has approved. Agents
// fetch each other's rosters over mutual TLS and adopt the members they don't
// know yet, so a machine that joins through one member of the house is soon
// trusted by all of them, without a code per pair.
//
// The roster is signed by its issuer as well as carried over a pinned
// channel, so what a machine was told, and by whom, can be kept and checked
// later, not just believed at the time.

// RosterPath is where an agent serves its roster, on the secured listener.
const RosterPath = "/v1/house/roster"

// rosterContext prefixes every signed roster. It keeps a roster signature from
// meaning anything else, and makes every signed roster longer than the nonce
// the prove endpoint will sign for a stranger.
const rosterContext = "jaggery house roster v1\x00"

// Member is one approved machine as its voucher knows it.
type Member struct {
	MachineID string       `json:"machine_id"`
	Name      string       `json:"name"`
	PublicKey []byte       `json:"public_key"`
	Addrs     []netip.Addr `json:"addrs,omitempty"`
	Port      int          `json:"port,omitempty"`
}

type Roster struct {
	Issuer   string    `json:"issuer"`
	IssuedAt time.Time `json:"issued_at"`
	Members  []Member  `json:"members"`
}

// SignedRoster carries the roster's exact bytes, so the signature is checked
// over what was signed rather than over a re-encoding of it.
type SignedRoster struct {
	Body      []byte `json:"body"`
	Signature []byte `json:"signature"`
}

func SignRoster(id *identity.Identity, r Roster) (SignedRoster, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return SignedRoster{}, fmt.Errorf("encoding roster: %w", err)
	}
	return SignedRoster{
		Body:      body,
		Signature: ed25519.Sign(id.PrivateKey, rosterMessage(body)),
	}, nil
}

var (
	ErrBadRoster    = errors.New("roster is not signed by the machine that sent it")
	ErrBadMemberKey = errors.New("roster lists a machine whose key does not match its id")
)

// OpenRoster checks a roster against the key of the machine that sent it: the
// signature must be that machine's, the roster must name it as the issuer, and
// every member's key must derive the id listed with it.
func OpenRoster(s SignedRoster, issuer ed25519.PublicKey) (Roster, error) {
	if len(issuer) != ed25519.PublicKeySize || !ed25519.Verify(issuer, rosterMessage(s.Body), s.Signature) {
		return Roster{}, ErrBadRoster
	}
	var r Roster
	if err := json.Unmarshal(s.Body, &r); err != nil {
		return Roster{}, fmt.Errorf("parsing roster: %w", err)
	}
	if r.Issuer != identity.IDFromKey(issuer) {
		return Roster{}, ErrBadRoster
	}
	for _, m := range r.Members {
		if len(m.PublicKey) != ed25519.PublicKeySize || identity.IDFromKey(m.PublicKey) != m.MachineID {
			return Roster{}, fmt.Errorf("%w: %s", ErrBadMemberKey, m.MachineID)
		}
	}
	return r, nil
}

func rosterMessage(body []byte) []byte {
	return append([]byte(rosterContext), body...)
}
