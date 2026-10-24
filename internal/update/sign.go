package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Releases are signed in CI with an ed25519 key held in the repo's release
// environment (tech-specs/releases.md, "Signing key setup"). An
// agent installs a build only if its release carries release.sig, made by
// one of the keys built in here, over the release's statement: what it is
// (release.json, less the withdrawn note) and every build's checksum
// (SHA256SUMS). That holds wherever the build came from, GitHub or a peer's
// relay, so a relay can no longer hand out code of its own.
//
// keys/main.pem signs releases; keys/spare.pem is the rotation key, whose
// private half is kept offline by the owner. If the main key leaks, CI
// switches to the spare and the next build drops the main key, without
// stranding machines already in the field.
//
// A withdrawal (scripts/withdraw.sh) edits release.json after the release is
// out, so it isn't signed: it can only stop an install, never start one.

const SigName = "release.sig"

//go:embed keys/*.pem
var keyFiles embed.FS

// testKey adds a public key (base64 DER, the middle line of its PEM) for the
// test house's own releases: -ldflags "-X .../internal/update.testKey=...".
// Releases never set it.
var testKey string

// testKeys are keys added by tests in other packages (TrustForTest).
var testKeys []ed25519.PublicKey

// TrustForTest trusts k as a release key for the rest of the test. Outside a
// test binary it does nothing.
func TrustForTest(t testing.TB, k ed25519.PublicKey) {
	if !testing.Testing() {
		return
	}
	testKeys = append(testKeys, k)
	t.Cleanup(func() { testKeys = nil })
}

var (
	ErrUnsigned     = errors.New("is not signed")
	ErrBadSignature = errors.New("is not signed by a Jaggery release key")
)

// trustedKeys are the keys a release may be signed with.
func trustedKeys() []ed25519.PublicKey {
	var keys []ed25519.PublicKey
	entries, _ := keyFiles.ReadDir("keys")
	for _, e := range entries {
		raw, err := keyFiles.ReadFile("keys/" + e.Name())
		if err != nil {
			continue
		}
		if b, _ := pem.Decode(raw); b != nil {
			if k, ok := parseKey(b.Bytes); ok {
				keys = append(keys, k)
			}
		}
	}
	if testKey != "" {
		if der, err := base64.StdEncoding.DecodeString(testKey); err == nil {
			if k, ok := parseKey(der); ok {
				keys = append(keys, k)
			}
		}
	}
	return append(keys, testKeys...)
}

func parseKey(der []byte) (ed25519.PublicKey, bool) {
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, false
	}
	ek, ok := k.(ed25519.PublicKey)
	return ek, ok
}

// Statement is what release.sig signs. scripts/sign-release.sh writes the
// same bytes; a change here must change there too (TestSignReleaseScript).
func Statement(m Manifest, sums []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "jaggery release v1\nversion %s\nchannel %s\ncommit %s\nbuilt_at %s\n",
		m.Version, m.Channel, m.Commit, m.BuiltAt.UTC().Format(time.RFC3339))
	b.Write(sums)
	return b.Bytes()
}

// Verify checks sig (release.sig: one line of base64) over m and sums.
func Verify(m Manifest, sums, sig []byte) error {
	name := m.Version
	if name == "" {
		name = "the release"
	}
	if len(bytes.TrimSpace(sig)) == 0 {
		return fmt.Errorf("%s %w; this build only installs signed releases", name, ErrUnsigned)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%s %w (unreadable %s)", name, ErrBadSignature, SigName)
	}
	msg := Statement(m, sums)
	for _, k := range trustedKeys() {
		if ed25519.Verify(k, msg, raw) {
			return nil
		}
	}
	return fmt.Errorf("%s %w", name, ErrBadSignature)
}

// IsSignatureError reports whether err is a release refused for its signature.
func IsSignatureError(err error) bool {
	return errors.Is(err, ErrUnsigned) || errors.Is(err, ErrBadSignature)
}
