package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signer is the key the fake GitHub signs with, trusted through testKey as
// the test house's builds trust theirs.
var signer = func() ed25519.PrivateKey {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	testKey = base64.StdEncoding.EncodeToString(der)
	return priv
}()

func sign(k ed25519.PrivateKey, m Manifest, sums []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(k, Statement(m, sums))) + "\n")
}

func TestTheReleaseKeysAreBuiltIn(t *testing.T) {
	saved := testKey
	testKey = ""
	defer func() { testKey = saved }()
	if n := len(trustedKeys()); n != 2 {
		t.Fatalf("%d built-in keys, want main and spare", n)
	}
}

func TestOnlyProperlySignedReleasesAreUsed(t *testing.T) {
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	m := Manifest{Version: "0.9.0-beta.2", Channel: "beta", Commit: "c2", BuiltAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)}
	build := map[string][]byte{"jag-linux-amd64": []byte("the real build")}
	cases := []struct {
		name   string
		tamper func(files map[string][]byte)
		want   error
	}{
		{"signed", func(map[string][]byte) {}, nil},
		{"no signature", func(f map[string][]byte) { delete(f, SigName) }, ErrUnsigned},
		{"another key", func(f map[string][]byte) { f[SigName] = sign(other, m, f[SumsName]) }, ErrBadSignature},
		{"checksums swapped", func(f map[string][]byte) {
			f[SumsName] = sums(map[string][]byte{"jag-linux-amd64": []byte("someone else's build")})
		}, ErrBadSignature},
		{"version changed", func(f map[string][]byte) {
			changed := m
			changed.Version = "0.9.0-beta.9"
			f[ManifestName], _ = json.Marshal(changed)
		}, ErrBadSignature},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gh := newFakeGitHub(t)
			files := map[string][]byte{"jag-linux-amd64": build["jag-linux-amd64"]}
			gh.add("v0.9.0-beta.2", true, time.Now(), m, files)
			before := map[string][]byte{}
			for k, v := range files {
				before[k] = v
			}
			c.tamper(files)
			for name := range before {
				if data, ok := files[name]; ok {
					gh.files["/assets/v0.9.0-beta.2/"+name] = data
				} else {
					delete(gh.files, "/assets/v0.9.0-beta.2/"+name)
				}
			}
			if _, ok := files[SigName]; !ok {
				gh.releases[0]["assets"] = withoutAsset(gh.releases[0]["assets"].([]map[string]string), SigName)
			}

			src := gh.source("secret")
			rel, err := src.Latest(context.Background(), "beta")
			if c.want == nil {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := src.Download(context.Background(), rel, "jag-linux-amd64"); err != nil {
					t.Fatal(err)
				}
				return
			}
			if !IsSignatureError(err) || !strings.Contains(err.Error(), c.want.Error()) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if _, err := src.ReleaseByTag(context.Background(), "v0.9.0-beta.2"); !IsSignatureError(err) {
				t.Fatalf("by tag: got %v", err)
			}
		})
	}
}

func withoutAsset(assets []map[string]string, name string) []map[string]string {
	var out []map[string]string
	for _, a := range assets {
		if a["name"] != name {
			out = append(out, a)
		}
	}
	return out
}

// A withdrawal edits release.json after signing; the release still reads as
// signed, and is skipped as withdrawn.
func TestWithdrawingKeepsTheSignatureValid(t *testing.T) {
	m := Manifest{Version: "0.9.0-beta.2", Channel: "beta", Commit: "c2"}
	sums := []byte("abc  jag-linux-amd64\n")
	sig := sign(signer, m, sums)
	m.Withdrawn = "crashes on the Pi"
	if err := Verify(m, sums, sig); err != nil {
		t.Fatal(err)
	}
}

// scripts/sign-release.sh, which CI signs with, writes the statement Go checks.
func TestSignReleaseScript(t *testing.T) {
	for _, tool := range []string{"openssl", "jq", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
	dir := t.TempDir()
	key, err := exec.Command("openssl", "genpkey", "-algorithm", "ed25519").Output()
	if err != nil {
		t.Skipf("this OpenSSL has no ed25519: %v", err)
	}
	pubPEM, err := runWithStdin(key, "openssl", "pkey", "-pubout")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(pubPEM)
	pub, ok := parseKey(b.Bytes)
	if !ok {
		t.Fatal("no public key")
	}
	TrustForTest(t, pub)

	built := "2026-10-17T14:02:03Z"
	manifest := `{"version":"0.9.0-beta.4","channel":"beta","commit":"4444","built_at":"` + built + `"}` + "\n"
	sums := []byte("0123abcd  jag-linux-amd64\n4567ef01  jag-darwin-arm64\n")
	_ = os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o600)
	_ = os.WriteFile(filepath.Join(dir, SumsName), sums, 0o600)
	cmd := exec.Command("sh", "../../scripts/sign-release.sh", dir)
	cmd.Env = append(os.Environ(), "JAGGERY_SIGNING_KEY="+string(key))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	sig, err := os.ReadFile(filepath.Join(dir, SigName))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		t.Fatal(err)
	}
	if err := Verify(m, sums, sig); err != nil {
		t.Fatalf("the script's signature doesn't verify: %v", err)
	}
}

func runWithStdin(in []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(string(in))
	return cmd.Output()
}
