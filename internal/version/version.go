// Package version is what this build is. Release builds set the variables
// with -ldflags -X (see .github/workflows/release.yml); a plain `go build`
// leaves them as a dev build, which is older than any release.
//
// Versions are SemVer 2.0 (tech-specs/releases.md): "0.5.0-beta.13" for a
// beta build, "0.5.0" for prod, "0.0.0-dev+<commit>" for a build made by hand.
// Builds from before SemVer called themselves "beta.<n>".
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const devVersion = "0.0.0-dev"

var (
	// Version is the SemVer version without its "v"; see the package comment.
	Version = devVersion
	// Commit is the full git commit the build came from. A dev build takes
	// it from Go's build info.
	Commit = ""
	// BuiltAt is when CI built it, RFC 3339 in UTC.
	BuiltAt = ""
	// Channel is "beta" or "prod"; a dev build follows beta.
	Channel = ""
)

// Built is BuiltAt parsed; zero for a dev build.
func Built() time.Time {
	t, _ := time.Parse(time.RFC3339, BuiltAt)
	return t
}

// Dev reports a build that didn't come from a release.
func Dev() bool { return Built().IsZero() }

// FollowedChannel is the release channel this build updates from.
func FollowedChannel() string {
	if Channel == "prod" {
		return "prod"
	}
	return "beta"
}

func init() {
	if Commit != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if Version == devVersion && Commit != "" {
		Version = devVersion + "+" + short(Commit)
		if modified {
			Version += ".dirty"
		}
	}
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// Valid reports a SemVer version (with or without its "v").
func Valid(v string) bool { return semver.IsValid(withV(v)) }

// Compare orders two SemVer versions like strings.Compare; both must be
// Valid. Pre-releases rank below their release: 0.5.0-beta.9 < 0.5.0.
func Compare(a, b string) int { return semver.Compare(withV(a), withV(b)) }

func withV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// Change classifies moving from version a to b: "major", "minor" or
// "patch" (which includes a pre-release of the same version). Both Valid.
func Change(a, b string) string {
	switch {
	case semver.Major(withV(a)) != semver.Major(withV(b)):
		return "major"
	case semver.MajorMinor(withV(a)) != semver.MajorMinor(withV(b)):
		return "minor"
	}
	return "patch"
}

// String is a one-line description, e.g. "0.5.0-beta.41 (9c16d76, built
// 2026-10-07 15:33)".
func String() string {
	if Dev() {
		if Commit != "" {
			return "dev build " + Version
		}
		return "dev build"
	}
	commit := Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	return fmt.Sprintf("%s (%s, built %s)", Version, commit, Built().Local().Format("2006-01-02 15:04"))
}

// Asset is the release file name for this platform, e.g. "jag-linux-armv7"
// or "jag-windows-amd64.exe".
func Asset() string {
	return AssetFor(runtime.GOOS, runtime.GOARCH, goarm())
}

func AssetFor(goos, goarch, goarm string) string {
	name := "jag-" + goos + "-" + goarch
	if goarch == "arm" {
		name = "jag-" + goos + "-armv" + goarm
	}
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// goarm is the ARM version this binary was built for, from its build info.
func goarm() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "GOARM" && s.Value != "" {
				return s.Value[:1] // "7" or "7,softfloat"
			}
		}
	}
	return "7"
}
