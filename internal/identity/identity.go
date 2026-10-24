package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrNotInitialized = errors.New("no identity on this machine yet: run 'jag up' first")

const fileName = "identity.json"

type Identity struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	PublicKey  ed25519.PublicKey  `json:"public_key"`
	PrivateKey ed25519.PrivateKey `json:"private_key"`
	CreatedAt  time.Time          `json:"created_at"`
}

// IDFromKey derives a machine's short id from its public key. A peer that
// receives a public key recomputes this and checks it against the advertised
// id, which is what binds a claimed id to a key it cannot forge.
func IDFromKey(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:6])
}

// FingerprintFromKey is the value a user compares when approving a machine; it
// commits to the whole key rather than the prefix the machine id uses.
func FingerprintFromKey(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

func (i *Identity) Fingerprint() string {
	return FingerprintFromKey(i.PublicKey)
}

func StateDir() (string, error) {
	if dir := os.Getenv("JAGGERY_STATE_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating state directory: %w", err)
	}
	return filepath.Join(base, "jaggery"), nil
}

func Load() (*Identity, error) {
	dir, err := StateDir()
	if err != nil {
		return nil, err
	}
	return loadFrom(filepath.Join(dir, fileName))
}

// LoadOrCreate returns the machine's identity, generating one on first run.
func LoadOrCreate() (id *Identity, created bool, err error) {
	dir, err := StateDir()
	if err != nil {
		return nil, false, err
	}

	path := filepath.Join(dir, fileName)
	switch existing, err := loadFrom(path); {
	case err == nil:
		return existing, false, nil
	case !errors.Is(err, ErrNotInitialized):
		return nil, false, err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("creating state directory: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("generating machine key: %w", err)
	}

	fresh := &Identity{
		ID:         IDFromKey(pub),
		Name:       defaultName(),
		PublicKey:  pub,
		PrivateKey: priv,
		CreatedAt:  time.Now().UTC(),
	}
	if err := save(path, fresh); err != nil {
		return nil, false, err
	}
	return fresh, true, nil
}

func loadFrom(path string) (*Identity, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("reading identity: %w", err)
	}

	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("parsing identity at %s: %w", path, err)
	}
	if len(id.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("identity at %s has a malformed private key", path)
	}
	return &id, nil
}

func save(path string, id *Identity) error {
	raw, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding identity: %w", err)
	}

	// Write-then-rename so a crash mid-write can't leave an unusable identity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("writing identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saving identity: %w", err)
	}
	return nil
}

func defaultName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unnamed"
	}
	return strings.ToLower(strings.TrimSuffix(host, ".local"))
}
