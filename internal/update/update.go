// Package update finds, checks and installs new builds of the agent from the
// repo's GitHub releases.
//
// CI publishes a release for every merge to beta (a prerelease tagged
// beta-<n>) and for every v* tag on prod. Each carries a build per platform,
// SHA256SUMS, and release.json saying which commit it is and when it was
// built. CI signs those two (release.sig, see sign.go), and a release is
// used only if the signature holds; a download is then accepted only if its
// checksum matches the signed SHA256SUMS.
//
// Releases are public; a GitHub token is optional and only raises GitHub's
// rate limit.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/version"
)

const (
	Repo         = "jaggery-dev/jaggery"
	ManifestName = "release.json"
	SumsName     = "SHA256SUMS"
	maxBinary    = 200 << 20
)

// Manifest is release.json: what a release is.
type Manifest struct {
	Version string    `json:"version"`
	Channel string    `json:"channel"`
	Commit  string    `json:"commit"`
	BuiltAt time.Time `json:"built_at"`
	// Withdrawn, when set, says why the release was pulled after it was
	// published (scripts/withdraw.sh). Agents skip it, and a machine
	// running it moves off it (releases.md).
	Withdrawn string `json:"withdrawn,omitempty"`
}

// Release is one GitHub release with its manifest read.
type Release struct {
	Tag      string
	Manifest Manifest
	// Sums and Sig are SHA256SUMS and release.sig, read once and verified
	// (Verified), so a download is checked against what was signed.
	Sums   []byte
	Sig    []byte
	assets map[string]string // name -> API download URL
	// versions tell apart two uploads at one URL: size and updated_at.
	versions map[string]string
}

// Verified fetches rel's SHA256SUMS and release.sig and checks the signature.
func (s Source) Verified(ctx context.Context, rel Release) (Release, error) {
	sums, err := s.asset(ctx, rel, SumsName)
	if err != nil {
		return Release{}, err
	}
	var sig []byte
	if _, ok := rel.assets[SigName]; ok {
		if sig, err = s.asset(ctx, rel, SigName); err != nil {
			return Release{}, err
		}
	}
	if err := Verify(rel.Manifest, sums, sig); err != nil {
		return Release{}, err
	}
	rel.Sums, rel.Sig = sums, sig
	return rel, nil
}

// Source reads releases from GitHub.
type Source struct {
	Repo  string
	Token string
	// API is the GitHub API base; tests point it at a fake.
	API  string
	HTTP *http.Client
	// Cache keeps the small signed files between checks; nil keeps nothing.
	Cache *AssetCache
}

// AssetCache keeps release.json, SHA256SUMS and release.sig between checks,
// so a check that finds nothing new costs GitHub one request (the release
// list) instead of four. That keeps a house of many machines behind one
// address under GitHub's limit of 60 unauthenticated requests an hour.
// Entries are keyed on the asset's URL, size and updated_at: GitHub gives a
// replaced asset (scripts/withdraw.sh) a new id and time, so a kept copy
// never goes stale. Builds aren't kept here; relay.go keeps those on disk.
type AssetCache struct {
	mu    sync.Mutex
	files map[string][]byte
}

const maxCachedAssets = 256

var sharedCache = &AssetCache{}

func (c *AssetCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.files[key]
	return data, ok
}

func (c *AssetCache) put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil || len(c.files) >= maxCachedAssets {
		c.files = map[string][]byte{}
	}
	c.files[key] = data
}

func cachable(name string) bool {
	return name == ManifestName || name == SumsName || name == SigName
}

// NewSource reads GitHub's releases, or those of the server named by
// $JAGGERY_RELEASES_API (a mirror, or the test house's fake release server).
func NewSource(token string) Source {
	api := "https://api.github.com"
	if a := strings.TrimRight(strings.TrimSpace(os.Getenv("JAGGERY_RELEASES_API")), "/"); a != "" {
		api = a
	}
	return Source{Repo: Repo, Token: token, API: api, HTTP: &http.Client{Timeout: 5 * time.Minute}, Cache: sharedCache}
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	CreatedAt  time.Time `json:"created_at"`
	Assets     []struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Size      int64  `json:"size"`
		UpdatedAt string `json:"updated_at"`
	} `json:"assets"`
}

var (
	ErrNoRelease = errors.New("no release on that channel yet")
	// ErrRateLimited: GitHub allows 60 unauthenticated requests an hour per
	// address, shared by every machine behind it.
	ErrRateLimited = errors.New("GitHub's rate limit for this address is used up")
	// ErrNeedToken: GitHub answers 404 for a private repo's releases
	// without a token (a fork or mirror kept private, or a moved repo).
	ErrNeedToken = errors.New("GitHub found no releases (404) and no token is set: if the release source is a private repo, save a read-only token in github-token in the state directory (see `jag update --help`)")
)

// Latest returns the newest release on channel: beta prereleases tagged
// beta-<n>, or prod's v* releases.
func (s Source) Latest(ctx context.Context, channel string) (Release, error) {
	rel, _, err := s.LatestAndWithdrawn(ctx, channel)
	return rel, err
}

// LatestAndWithdrawn is Latest, plus the newer releases it passed over
// because they were withdrawn, so the agent can tell the house.
func (s Source) LatestAndWithdrawn(ctx context.Context, channel string) (Release, []Manifest, error) {
	body, err := s.get(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=50", s.API, s.Repo), "application/vnd.github+json")
	if err != nil {
		return Release{}, nil, err
	}
	var all []ghRelease
	if err := json.Unmarshal(body, &all); err != nil {
		return Release{}, nil, fmt.Errorf("reading releases: %w", err)
	}

	var candidates []ghRelease
	for _, r := range all {
		if !r.Draft && onChannel(r, channel) {
			candidates = append(candidates, r)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.After(candidates[j].CreatedAt) })
	// The newest that hasn't been withdrawn; a withdrawn release is passed
	// over for the one before it. One that isn't properly signed is an
	// error, not skipped: something is wrong, and an older one won't do.
	var withdrawn []Manifest
	for _, r := range candidates {
		rel, err := s.withManifest(ctx, r)
		if err != nil {
			return Release{}, withdrawn, err
		}
		if rel.Manifest.Withdrawn == "" {
			rel, err = s.Verified(ctx, rel)
			return rel, withdrawn, err
		}
		withdrawn = append(withdrawn, rel.Manifest)
	}
	return Release{}, withdrawn, ErrNoRelease
}

func (s Source) withManifest(ctx context.Context, r ghRelease) (Release, error) {
	rel := Release{Tag: r.TagName, assets: map[string]string{}, versions: map[string]string{}}
	for _, a := range r.Assets {
		rel.assets[a.Name] = a.URL
		rel.versions[a.Name] = fmt.Sprintf("%d|%s", a.Size, a.UpdatedAt)
	}
	raw, err := s.asset(ctx, rel, ManifestName)
	if err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(raw, &rel.Manifest); err != nil {
		return Release{}, fmt.Errorf("reading %s of %s: %w", ManifestName, rel.Tag, err)
	}
	return rel, nil
}

// onChannel: prod is a non-prerelease vX.Y.Z tag. Beta is a prerelease,
// tagged either the old way (beta-N) or with SemVer (v0.5.0-beta.N), so
// agents keep finding beta builds across the switch (releases.md).
func onChannel(r ghRelease, channel string) bool {
	if channel == "prod" {
		return !r.Prerelease && version.Valid(r.TagName) && !strings.Contains(r.TagName, "-")
	}
	if !r.Prerelease {
		return false
	}
	return strings.HasPrefix(r.TagName, "beta-") || (version.Valid(r.TagName) && strings.Contains(r.TagName, "-beta."))
}

// ReleaseByTag reads one release, for installing a specific version rather
// than the newest.
func (s Source) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	body, err := s.get(ctx, fmt.Sprintf("%s/repos/%s/releases/tags/%s", s.API, s.Repo, tag), "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	var r ghRelease
	if err := json.Unmarshal(body, &r); err != nil {
		return Release{}, fmt.Errorf("reading release %s: %w", tag, err)
	}
	rel, err := s.withManifest(ctx, r)
	if err != nil {
		return Release{}, err
	}
	return s.Verified(ctx, rel)
}

// TagsFor lists the tags a version may be published under: SemVer's own,
// and for a beta build the older beta-N form (releases.md).
func TagsFor(v string) []string {
	tags := []string{"v" + strings.TrimPrefix(v, "v")}
	if i := strings.LastIndex(v, "-beta."); i >= 0 {
		tags = append(tags, "beta-"+v[i+len("-beta."):])
	}
	return tags
}

var ErrChecksum = errors.New("download does not match SHA256SUMS")

// Download fetches name from rel and checks it against the release's signed
// SHA256SUMS.
func (s Source) Download(ctx context.Context, rel Release, name string) ([]byte, error) {
	sums := rel.Sums
	if sums == nil {
		return nil, fmt.Errorf("%s: %w", rel.Tag, ErrUnsigned)
	}
	if _, ok := ParseSums(sums)[name]; !ok {
		return nil, fmt.Errorf("%s has no build for this machine (%s)", rel.Tag, name)
	}
	data, err := s.asset(ctx, rel, name)
	if err != nil {
		return nil, err
	}
	return data, CheckSum(sums, name, data)
}

// CheckSum checks data against name's line in SHA256SUMS.
func CheckSum(sums []byte, name string, data []byte) error {
	want, ok := ParseSums(sums)[name]
	if !ok {
		return fmt.Errorf("no checksum for %s", name)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s: %w", name, ErrChecksum)
	}
	return nil
}

// ParseSums reads `sha256sum` output: "<hex>  <name>" per line.
func ParseSums(raw []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 {
			out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return out
}

func (s Source) asset(ctx context.Context, rel Release, name string) ([]byte, error) {
	url, ok := rel.assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Tag, name)
	}
	keep := s.Cache != nil && cachable(name)
	key := url + "|" + rel.versions[name]
	if keep {
		if data, ok := s.Cache.get(key); ok {
			return data, nil
		}
	}
	data, err := s.get(ctx, url, "application/octet-stream")
	if err == nil && keep {
		s.Cache.put(key, data)
	}
	return data, err
}

func (s Source) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if s.Token != "" {
		// Go drops this header when GitHub redirects the download to its
		// storage host, which is what we want: the redirect is pre-signed.
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound && s.Token == "":
		return nil, ErrNeedToken
	case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
		resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, rateLimited(resp.Header.Get("X-RateLimit-Reset"))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub answered %s for %s", resp.Status, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBinary))
}

// rateLimited says until when, from GitHub's X-RateLimit-Reset (Unix time).
func rateLimited(reset string) error {
	hint := "a token raises it (see `jag update --help`); until then the house can fetch from a machine that keeps the build"
	if secs, err := strconv.ParseInt(reset, 10, 64); err == nil {
		return fmt.Errorf("%w until %s; %s", ErrRateLimited, time.Unix(secs, 0).Format("15:04"), hint)
	}
	return fmt.Errorf("%w; %s", ErrRateLimited, hint)
}

// Running is the build that's running now.
type Running struct {
	Version string
	Commit  string
	BuiltAt time.Time
}

// Newer reports whether a release should replace the running build. A dev
// build is replaced by any release, and the same commit never replaces
// itself. When both versions are SemVer they decide; builds from before
// SemVer ("beta.12") fall back to the later build time.
func Newer(m Manifest, run Running) bool {
	if run.BuiltAt.IsZero() {
		return true
	}
	if m.Commit == run.Commit {
		return false
	}
	if version.Valid(m.Version) && version.Valid(run.Version) {
		return version.Compare(m.Version, run.Version) > 0
	}
	return m.BuiltAt.After(run.BuiltAt)
}

// RunningNow describes this binary.
func RunningNow() Running {
	return Running{Version: version.Version, Commit: version.Commit, BuiltAt: version.Built()}
}

// Install replaces the binary at exe with data, keeping the old one as
// exe+".prev" for Rollback. The new binary must run (`jag version`) before it
// goes in, which also catches a build for the wrong architecture.
func Install(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".jag-update-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s (%w); reinstall once with scripts/install.sh so the agent owns its binary", exe, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // a no-op once renamed into place
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := Runs(tmpName); err != nil {
		return err
	}
	if err := os.Rename(exe, exe+".prev"); err != nil {
		return fmt.Errorf("keeping the current binary: %w", err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		_ = os.Rename(exe+".prev", exe)
		return fmt.Errorf("installing: %w", err)
	}
	return nil
}

// Runs checks that the binary at path starts and reports its version.
func Runs(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, path, "version").CombinedOutput(); err != nil {
		return fmt.Errorf("the new binary does not run here: %v %s", err, bytes.TrimSpace(out))
	}
	return nil
}

var ErrNoPrevious = errors.New("no previous version kept to roll back to")

// Rollback swaps exe with exe+".prev", so running it again rolls forward.
func Rollback(exe string) error {
	prev := exe + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return ErrNoPrevious
	}
	if err := Runs(prev); err != nil {
		return err
	}
	swap := exe + ".swap"
	if err := os.Rename(exe, swap); err != nil {
		return err
	}
	if err := os.Rename(prev, exe); err != nil {
		_ = os.Rename(swap, exe)
		return err
	}
	return os.Rename(swap, prev)
}

// Token finds an optional GitHub token: $JAGGERY_GITHUB_TOKEN, $GITHUB_TOKEN,
// a github-token file in the state directory, or the gh CLI's. Empty when
// there is none, which is the usual case: releases are public.
func Token(stateDir string) string {
	for _, env := range []string{"JAGGERY_GITHUB_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t
		}
	}
	if raw, err := os.ReadFile(filepath.Join(stateDir, "github-token")); err == nil {
		if t := strings.TrimSpace(string(raw)); t != "" {
			return t
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
