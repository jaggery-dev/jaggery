package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGitHub serves a release list and its assets the way the GitHub API does.
type fakeGitHub struct {
	srv      *httptest.Server
	releases []map[string]any
	files    map[string][]byte // asset path -> content
	requests int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.NotFound(w, r) // private repo
			return
		}
		if r.URL.Path == "/repos/o/r/releases" {
			_ = json.NewEncoder(w).Encode(f.releases)
			return
		}
		for _, rel := range f.releases {
			if r.URL.Path == "/repos/o/r/releases/tags/"+rel["tag_name"].(string) {
				_ = json.NewEncoder(w).Encode(rel)
				return
			}
		}
		data, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// add publishes a release signed with the test's key (signed); a test that
// wants it unsigned or wrongly signed changes the files afterwards.
func (f *fakeGitHub) add(tag string, prerelease bool, created time.Time, m Manifest, files map[string][]byte) {
	var assets []map[string]string
	if _, ok := files[SumsName]; !ok {
		files[SumsName] = sums(files)
	}
	files[SigName] = sign(signer, m, files[SumsName])
	files[ManifestName], _ = json.Marshal(m)
	for name, data := range files {
		path := "/assets/" + tag + "/" + name
		f.files[path] = data
		assets = append(assets, map[string]string{"name": name, "url": f.srv.URL + path})
	}
	f.releases = append(f.releases, map[string]any{
		"tag_name": tag, "prerelease": prerelease, "created_at": created, "assets": assets,
	})
}

func (f *fakeGitHub) source(token string) Source {
	return Source{Repo: "o/r", Token: token, API: f.srv.URL, HTTP: f.srv.Client()}
}

func sums(files map[string][]byte) []byte {
	out := ""
	for name, data := range files {
		sum := sha256.Sum256(data)
		out += fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return []byte(out)
}

func TestLatestPicksTheNewestReleaseOnEachChannel(t *testing.T) {
	gh := newFakeGitHub(t)
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	gh.add("beta-40", true, day, Manifest{Version: "beta.40", Commit: "aaa"}, map[string][]byte{})
	gh.add("beta-41", true, day.Add(time.Hour), Manifest{Version: "beta.41", Commit: "bbb"}, map[string][]byte{})
	gh.add("v0.4.0", false, day.Add(-48*time.Hour), Manifest{Version: "0.4.0", Commit: "ccc"}, map[string][]byte{})

	src := gh.source("secret")
	beta, err := src.Latest(context.Background(), "beta")
	if err != nil || beta.Manifest.Version != "beta.41" {
		t.Fatalf("beta: got %+v, %v", beta.Manifest, err)
	}
	prod, err := src.Latest(context.Background(), "prod")
	if err != nil || prod.Manifest.Version != "0.4.0" {
		t.Fatalf("prod: got %+v, %v", prod.Manifest, err)
	}
	if _, err := gh.source("").Latest(context.Background(), "beta"); !errors.Is(err, ErrNeedToken) {
		t.Fatalf("no token: got %v, want ErrNeedToken", err)
	}
}

func TestDownloadChecksTheChecksum(t *testing.T) {
	gh := newFakeGitHub(t)
	good := map[string][]byte{"jag-linux-amd64": []byte("the real build")}
	files := map[string][]byte{"jag-linux-amd64": []byte("the real build"), SumsName: sums(good)}
	gh.add("beta-1", true, time.Now(), Manifest{Version: "beta.1"}, files)

	src := gh.source("secret")
	rel, err := src.Latest(context.Background(), "beta")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := src.Download(context.Background(), rel, "jag-linux-amd64"); err != nil || string(data) != "the real build" {
		t.Fatalf("got %q, %v", data, err)
	}

	gh.files["/assets/beta-1/jag-linux-amd64"] = []byte("swapped on the way")
	if _, err := src.Download(context.Background(), rel, "jag-linux-amd64"); !errors.Is(err, ErrChecksum) {
		t.Fatalf("tampered download: got %v, want ErrChecksum", err)
	}
	if _, err := src.Download(context.Background(), rel, "jag-plan9-amd64"); err == nil {
		t.Fatal("downloaded a build the release doesn't list")
	}
}

func TestNewer(t *testing.T) {
	built := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	run := Running{Version: "0.5.0-beta.13", Commit: "aaa", BuiltAt: built}
	cases := []struct {
		name string
		m    Manifest
		run  Running
		want bool
	}{
		{"next beta", Manifest{Version: "0.5.0-beta.14", Commit: "bbb"}, run, true},
		// Numeric, not alphabetical: beta.9 is older than beta.13.
		{"older beta", Manifest{Version: "0.5.0-beta.9", Commit: "ccc", BuiltAt: built.Add(time.Hour)}, run, false},
		{"the release it leads to", Manifest{Version: "0.5.0", Commit: "ddd"}, run, true},
		{"next minor's beta", Manifest{Version: "0.6.0-beta.20", Commit: "eee"}, run, true},
		{"same commit, rebuilt", Manifest{Version: "0.5.0", Commit: "aaa"}, run, false},
		// From before SemVer, today's agents: build time decides.
		{"semver release seen by a beta.11 agent", Manifest{Version: "0.5.0-beta.14", Commit: "bbb", BuiltAt: built.Add(time.Hour)},
			Running{Version: "beta.11", Commit: "zzz", BuiltAt: built}, true},
		{"old-style release seen by a semver agent", Manifest{Version: "beta.12", Commit: "yyy", BuiltAt: built.Add(-time.Hour)}, run, false},
		{"dev build takes any release", Manifest{Version: "0.4.0", Commit: "fff"}, Running{Version: "0.0.0-dev+abc1234"}, true},
	}
	for _, c := range cases {
		if got := Newer(c.m, c.run); got != c.want {
			t.Errorf("%s: Newer = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBothBetaTagStylesAreOnTheBetaChannel(t *testing.T) {
	cases := []struct {
		tag        string
		prerelease bool
		beta, prod bool
	}{
		{"beta-12", true, true, false},
		{"v0.5.0-beta.13", true, true, false},
		{"v0.5.0", false, false, true},
		{"v0.4.0", false, false, true},
		{"v0.5.0-rc.1", true, false, false},
		{"nightly", true, false, false},
	}
	for _, c := range cases {
		r := ghRelease{TagName: c.tag, Prerelease: c.prerelease}
		if onChannel(r, "beta") != c.beta || onChannel(r, "prod") != c.prod {
			t.Errorf("%s: beta %v prod %v, want %v %v", c.tag, onChannel(r, "beta"), onChannel(r, "prod"), c.beta, c.prod)
		}
	}
}

func script(version string) []byte {
	return []byte("#!/bin/sh\necho " + version + "\n")
}

func TestInstallKeepsThePreviousVersionAndRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts as stand-in binaries")
	}
	exe := filepath.Join(t.TempDir(), "jag")
	if err := os.WriteFile(exe, script("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(exe, script("two")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(script("two")) {
		t.Fatalf("installed %q", got)
	}
	if got, _ := os.ReadFile(exe + ".prev"); string(got) != string(script("one")) {
		t.Fatalf("kept %q as previous", got)
	}

	if err := Rollback(exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(script("one")) {
		t.Fatalf("after rollback running %q", got)
	}
	if err := Rollback(exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(script("two")) {
		t.Fatalf("rolling forward again gave %q", got)
	}
}

// A build that won't start (wrong architecture, truncated) never replaces a
// working one.
func TestInstallRefusesABinaryThatDoesNotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts as stand-in binaries")
	}
	exe := filepath.Join(t.TempDir(), "jag")
	if err := os.WriteFile(exe, script("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(exe, []byte("#!/bin/sh\nexit 3\n")); err == nil {
		t.Fatal("installed a binary that fails to run")
	}
	if got, _ := os.ReadFile(exe); string(got) != string(script("one")) {
		t.Fatalf("the working binary was replaced: %q", got)
	}
	if _, err := os.Stat(exe + ".prev"); err == nil {
		t.Fatal("a failed install left a .prev behind")
	}
}

func TestParseSums(t *testing.T) {
	got := ParseSums([]byte("ABC123  jag-linux-amd64\ndef456 *jag-windows-amd64.exe\n\n"))
	if got["jag-linux-amd64"] != "abc123" || got["jag-windows-amd64.exe"] != "def456" {
		t.Fatalf("got %v", got)
	}
}

// A withdrawn release is passed over for the one before it, and reported.
func TestLatestSkipsWithdrawnReleases(t *testing.T) {
	gh := newFakeGitHub(t)
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	gh.add("v0.5.0-beta.20", true, day, Manifest{Version: "0.5.0-beta.20", Commit: "c20"}, map[string][]byte{})
	gh.add("v0.5.0-beta.21", true, day.Add(time.Hour), Manifest{Version: "0.5.0-beta.21", Commit: "c21", Withdrawn: "crashes on the Pi"}, map[string][]byte{})
	rel, withdrawn, err := gh.source("secret").LatestAndWithdrawn(context.Background(), "beta")
	if err != nil || rel.Manifest.Version != "0.5.0-beta.20" {
		t.Fatalf("got %+v, %v; want beta.20", rel.Manifest, err)
	}
	if len(withdrawn) != 1 || withdrawn[0].Commit != "c21" {
		t.Fatalf("withdrawn = %+v", withdrawn)
	}
}

func TestMarkWithdrawnOnce(t *testing.T) {
	dir := t.TempDir()
	_ = MarkWithdrawn(dir, "c21", "c21", "")
	_ = MarkWithdrawn(dir, "c22")
	if got := Withdrawn(dir); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestACheckWithNothingNewCostsOneRequest(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.add("v0.9.0", false, time.Now(), Manifest{Version: "0.9.0", Commit: "aaa"}, map[string][]byte{})
	src := gh.source("secret")
	src.Cache = &AssetCache{}

	if _, err := src.Latest(context.Background(), "prod"); err != nil {
		t.Fatal(err)
	}
	if gh.requests != 4 { // the list, release.json, SHA256SUMS, release.sig
		t.Fatalf("first check: %d requests, want 4", gh.requests)
	}
	gh.requests = 0
	if _, err := src.Latest(context.Background(), "prod"); err != nil {
		t.Fatal(err)
	}
	if gh.requests != 1 {
		t.Fatalf("second check: %d requests, want 1 (the list)", gh.requests)
	}
}

func TestAReplacedAssetIsFetchedAgain(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.add("v0.9.0", false, time.Now(), Manifest{Version: "0.9.0", Commit: "aaa"}, map[string][]byte{})
	src := gh.source("secret")
	src.Cache = &AssetCache{}
	if _, err := src.Latest(context.Background(), "prod"); err != nil {
		t.Fatal(err)
	}

	// Withdrawn the way GitHub shows a re-upload: a new updated_at.
	path := "/assets/v0.9.0/" + ManifestName
	gh.files[path], _ = json.Marshal(Manifest{Version: "0.9.0", Commit: "aaa", Withdrawn: "crashes"})
	for _, a := range gh.releases[0]["assets"].([]map[string]string) {
		if a["name"] == ManifestName {
			a["updated_at"] = "2026-10-23T12:00:00Z"
		}
	}
	_, withdrawn, err := src.LatestAndWithdrawn(context.Background(), "prod")
	if !errors.Is(err, ErrNoRelease) || len(withdrawn) != 1 {
		t.Fatalf("got %v, %d withdrawn; want the release seen as withdrawn", err, len(withdrawn))
	}
}

func TestRateLimitSaysUntilWhen(t *testing.T) {
	reset := time.Date(2026, 10, 23, 15, 4, 0, 0, time.Local)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
		http.Error(w, `{"message": "API rate limit exceeded"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	src := Source{Repo: "o/r", API: srv.URL, HTTP: srv.Client()}
	_, err := src.Latest(context.Background(), "prod")
	if !errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "until 15:04") {
		t.Fatalf("got %v", err)
	}
}
