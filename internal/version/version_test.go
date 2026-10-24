package version

import (
	"os"
	"strings"
	"testing"
)

func TestAssetForEveryReleaseTarget(t *testing.T) {
	cases := map[[3]string]string{
		{"linux", "amd64", ""}:   "jag-linux-amd64",
		{"linux", "arm64", ""}:   "jag-linux-arm64",
		{"linux", "arm", "7"}:    "jag-linux-armv7",
		{"darwin", "arm64", ""}:  "jag-darwin-arm64",
		{"darwin", "amd64", ""}:  "jag-darwin-amd64",
		{"windows", "amd64", ""}: "jag-windows-amd64.exe",
	}
	for in, want := range cases {
		if got := AssetFor(in[0], in[1], in[2]); got != want {
			t.Errorf("AssetFor(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestDevBuildFollowsBeta(t *testing.T) {
	if !Dev() || FollowedChannel() != "beta" || String() != "dev build" {
		t.Fatalf("an unstamped build should be a dev build on beta: %q, %q", FollowedChannel(), String())
	}
}

func TestSemVerOrdering(t *testing.T) {
	ordered := []string{"0.0.0-dev+abc1234", "0.4.0", "0.5.0-beta.9", "0.5.0-beta.13", "0.5.0", "0.5.1", "0.6.0-beta.1", "1.0.0"}
	for i := 1; i < len(ordered); i++ {
		if Compare(ordered[i-1], ordered[i]) >= 0 {
			t.Errorf("%s should come before %s", ordered[i-1], ordered[i])
		}
	}
	if Valid("beta.12") || !Valid("0.5.0-beta.13") || !Valid("v0.5.0") {
		t.Error("Valid disagrees with SemVer")
	}
}

// CI stamps releases from VERSION, so it must always be a plain release
// version: the next one.
func TestVersionFileIsAReleaseVersion(t *testing.T) {
	raw, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	v := strings.TrimSpace(string(raw))
	if !Valid(v) || strings.ContainsAny(v, "-+") {
		t.Fatalf("VERSION is %q, want a plain X.Y.Z", v)
	}
}

func TestChange(t *testing.T) {
	cases := [][3]string{
		{"0.5.0", "0.5.1", "patch"}, {"0.5.0-beta.3", "0.5.0", "patch"},
		{"0.5.1", "0.6.0", "minor"}, {"1.2.3", "2.0.0", "major"},
	}
	for _, c := range cases {
		if got := Change(c[0], c[1]); got != c[2] {
			t.Errorf("Change(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
	}
}
