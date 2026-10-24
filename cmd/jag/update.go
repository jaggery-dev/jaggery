package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/update"
	"github.com/jaggery-dev/jaggery/internal/version"
)

func showVersion() error {
	fmt.Printf("jag %s\n", version.String())
	fmt.Printf("platform     %s/%s (release file %s)\n", runtime.GOOS, runtime.GOARCH, version.Asset())
	fmt.Printf("updates from %s\n", version.FollowedChannel())
	return nil
}

const updateHelp = `usage: jag update [--check] [--channel beta|prod] [--rollback]
       jag update --policy auto|notify|off [--channel beta|prod]

Replaces this machine's agent with the newest release on its channel (beta
builds follow beta, prod builds follow prod). The running agent notices and
restarts itself into the new version within a few seconds.

The running agent also checks every 30 minutes. With --policy auto it
installs new releases itself: the machine answering for jaggery.local goes
first, the rest 30 minutes later, and a build that won't start is rolled back
and skipped by the whole house. notify only reports them; off stops checking.
Beta builds default to auto, prod and dev builds to notify.

Without GitHub access it asks a machine you approved here that keeps the
build. Releases are public and need no token. One is used if present
($JAGGERY_GITHUB_TOKEN, $GITHUB_TOKEN, a github-token file in the state
directory, or a logged-in gh), which raises GitHub's rate limit for a house
with many machines behind one address.
`

// selfUpdate is `jag update`.
func selfUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, updateHelp) }
	check := fs.Bool("check", false, "only say whether an update is available")
	channel := fs.String("channel", version.FollowedChannel(), "beta or prod")
	rollback := fs.Bool("rollback", false, "go back to the version before the last update")
	policy := fs.String("policy", "", "set what the agent does with new releases: auto, notify or off")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if jsonOutput && (!*check || *policy != "" || *rollback) {
		return usagef("jag update takes --json only with --check: installing and rolling back stay at a terminal")
	}
	if *policy != "" {
		return setUpdatePolicy(*policy, *channel)
	}
	if *channel != "beta" && *channel != "prod" {
		return fmt.Errorf("--channel must be beta or prod")
	}

	exe, err := executable()
	if err != nil {
		return err
	}
	if *rollback {
		if err := update.Rollback(exe); err != nil {
			return err
		}
		fmt.Println("rolled back; run `jag update --rollback` again to undo")
		return nil
	}

	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	m, fetch, from, err := latestForCLI(ctx, dir, *channel)
	if err != nil {
		return err
	}
	if jsonOutput {
		return emitJSON(map[string]any{
			"running": version.Version, "channel": *channel, "latest": m, "from": from,
			"available": update.Newer(m, update.RunningNow()),
		})
	}
	fmt.Printf("running  %s\n", version.String())
	fmt.Printf("latest   %s (%s, built %s) on %s, from %s\n", m.Version, short(m.Commit),
		m.BuiltAt.Local().Format("2006-01-02 15:04"), *channel, from)
	if !update.Newer(m, update.RunningNow()) {
		fmt.Println("up to date")
		return nil
	}
	if *check {
		fmt.Println("an update is available: run `jag update`")
		return nil
	}

	data, err := fetch(ctx)
	if err != nil {
		return err
	}
	if err := update.Install(exe, data); err != nil {
		return err
	}
	fmt.Printf("installed %s at %s (signature and checksum verified)\n", m.Version, exe)
	fmt.Println("an agent running from this file restarts into it within a few seconds")
	return nil
}

// latestForCLI finds the newest release for `jag update`: from GitHub, or,
// on a machine that can't reach it (no token, no internet), from a directly
// approved peer that keeps it, the way the agent does.
func latestForCLI(ctx context.Context, dir, channel string) (update.Manifest, func(context.Context) ([]byte, error), string, error) {
	src := update.NewSource(update.Token(dir))
	rel, err := src.Latest(ctx, channel)
	if err == nil {
		return rel.Manifest, func(ctx context.Context) ([]byte, error) {
			return src.Download(ctx, rel, version.Asset())
		}, "GitHub", nil
	}
	id, idErr := identity.Load()
	store, storeErr := openStore()
	if idErr != nil || storeErr != nil {
		return update.Manifest{}, nil, "", err
	}
	r, peer, perr := peerRelease(ctx, id, store, channel, "")
	if perr != nil {
		return update.Manifest{}, nil, "", fmt.Errorf("%v; %v", err, perr)
	}
	return r.Manifest, func(ctx context.Context) ([]byte, error) {
		return peerDownload(ctx, id, peer, r, version.Asset())
	}, peer.Name, nil
}

func setUpdatePolicy(policy, channel string) error {
	p, err := update.ParsePolicy(policy)
	if err != nil {
		return err
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	s := update.Settings{Policy: p, Channel: channel}
	if err := update.SaveSettings(dir, s); err != nil {
		return err
	}
	fmt.Printf("updates: %s, from %s\n", s.Policy, s.Channel)
	return nil
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// startPath is where this binary was when the process started. Resolved
// once: after an update renames the running file to .prev, the OS reports
// that name as the executable, and a restart must run what is now at the
// original path instead.
var startPath, startPathErr = resolveExecutable()

// executable is this binary's real path, through any symlink (the installer
// links /usr/local/bin/jag to the copy the service user owns).
func executable() (string, error) { return startPath, startPathErr }

func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

const selfCheckInterval = 10 * time.Second

// errRestart ends `jag up` so main can start the new binary in its place.
var errRestart = errors.New("the agent's binary was replaced")

// watchSelf cancels the agent when its binary is replaced by a build that
// runs, so it restarts into the new version: `jag update`, or the installer.
func watchSelf(ctx context.Context, restart context.CancelCauseFunc) {
	exe, err := executable()
	if err != nil {
		return
	}
	start, err := os.Stat(exe)
	if err != nil {
		return
	}
	ticker := time.NewTicker(selfCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now, err := os.Stat(exe)
		if err != nil || os.SameFile(start, now) && now.ModTime().Equal(start.ModTime()) {
			continue
		}
		if err := update.Runs(exe); err != nil {
			fmt.Fprintln(os.Stderr, "warning: the agent's binary changed but the new one doesn't run; staying on this version:", err)
			start = now
			continue
		}
		fmt.Println("a new version was installed; restarting into it")
		if dir, err := identity.StateDir(); err == nil {
			_ = update.SavePending(dir, update.Pending{From: version.Commit, At: time.Now().UTC()})
		}
		restart(errRestart)
		return
	}
}
