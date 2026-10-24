package identity

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReportsUninitialized(t *testing.T) {
	t.Setenv("JAGGERY_STATE_DIR", t.TempDir())

	if _, err := Load(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("want ErrNotInitialized, got %v", err)
	}
}

func TestLoadOrCreateIsIdempotent(t *testing.T) {
	t.Setenv("JAGGERY_STATE_DIR", t.TempDir())

	first, created, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("first LoadOrCreate: %v", err)
	}
	if !created {
		t.Error("first call should report the identity as created")
	}

	second, created, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if created {
		t.Error("second call must not regenerate the identity")
	}

	if first.ID != second.ID {
		t.Errorf("machine id changed across loads: %s then %s", first.ID, second.ID)
	}
	if !bytes.Equal(first.PrivateKey, second.PrivateKey) {
		t.Error("private key changed across loads")
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Error("fingerprint is not stable across loads")
	}
}

func TestIdentityIsUsableForSigning(t *testing.T) {
	t.Setenv("JAGGERY_STATE_DIR", t.TempDir())

	id, _, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	msg := []byte("pair with me")
	sig := ed25519.Sign(id.PrivateKey, msg)
	if !ed25519.Verify(id.PublicKey, msg, sig) {
		t.Error("persisted keypair failed to round-trip a signature")
	}
}

func TestPrivateKeyIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)

	if _, _, err := LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat identity: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("identity file perms are %o, want 600", perm)
	}
}

func TestCorruptIdentityIsReportedNotReplaced(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"id":"x"}`), 0o600); err != nil {
		t.Fatalf("seeding corrupt identity: %v", err)
	}

	if _, _, err := LoadOrCreate(); err == nil {
		t.Fatal("want an error for a malformed identity, got nil")
	}
}
