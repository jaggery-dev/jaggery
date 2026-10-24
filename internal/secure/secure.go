// Package secure builds the mutual-TLS transport agents use to talk to each
// other. There is no certificate authority: every machine signs its own
// certificate with its ed25519 identity key, and trust is decided by pinning —
// a connection is accepted only if the peer's certificate carries a key the
// local trust store has approved. The certificate's key is the identity key,
// so pinning the certificate is pinning the machine.
package secure

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

// Approver reports whether a peer presenting this public key is allowed to
// connect. The trust store implements it; kept as an interface so secure does
// not import home and can be tested with a stub.
type Approver interface {
	IsApproved(key ed25519.PublicKey) bool
}

// certificate wraps the identity key in a minimal self-signed X.509 cert so it
// can be used with crypto/tls. The contents barely matter — verification is by
// key pinning, not by chain or name — but the key must be the identity key.
func certificate(id *identity.Identity) (tls.Certificate, error) {
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: id.ID},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, id.PublicKey, id.PrivateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("creating certificate: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  id.PrivateKey,
		Leaf:        mustParse(der),
	}, nil
}

func mustParse(der []byte) *x509.Certificate {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		// CreateCertificate just produced this; a parse failure is a bug.
		panic(err)
	}
	return cert
}

var errNotApproved = errors.New("peer is not an approved machine in this home")

// peerKey pulls the ed25519 public key out of the single certificate a peer
// presented, rejecting anything that is not exactly one ed25519 leaf.
func peerKey(rawCerts [][]byte) (ed25519.PublicKey, error) {
	if len(rawCerts) != 1 {
		return nil, fmt.Errorf("peer presented %d certificates, want exactly 1", len(rawCerts))
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return nil, fmt.Errorf("parsing peer certificate: %w", err)
	}
	key, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("peer certificate is not an ed25519 key")
	}
	return key, nil
}

// verifyApproved is the pinning check used on both ends of a connection.
func verifyApproved(approver Approver) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		key, err := peerKey(rawCerts)
		if err != nil {
			return err
		}
		if !approver.IsApproved(key) {
			return errNotApproved
		}
		return nil
	}
}

// ServerConfig is for the agent's listener: require a client certificate and
// accept it only if the client's key is approved.
func ServerConfig(id *identity.Identity, approver Approver) (*tls.Config, error) {
	cert, err := certificate(id)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		// We do our own check instead of CA-chain verification.
		VerifyPeerCertificate: verifyApproved(approver),
	}, nil
}

// ClientConfig is for dialing a peer: present our certificate and accept the
// server only if its key matches the one we expect for that machine.
func ClientConfig(id *identity.Identity, expect ed25519.PublicKey) (*tls.Config, error) {
	cert, err := certificate(id)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // no CA; we pin the exact key below
		VerifyPeerCertificate: verifyApproved(pinnedKey{expect}),
	}, nil
}

// pinnedKey approves exactly one key — the server we meant to reach.
type pinnedKey struct{ want ed25519.PublicKey }

func (p pinnedKey) IsApproved(key ed25519.PublicKey) bool {
	return p.want.Equal(key)
}
