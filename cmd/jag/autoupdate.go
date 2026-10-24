package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/update"
	"github.com/jaggery-dev/jaggery/internal/version"
)

var (
	updateFirst    = 2 * time.Minute  // let the house settle after a start
	updateInterval = 30 * time.Minute // how long a merge takes to reach the house
	// updateStagger is how long the rest of the house waits after a build is
	// published, so the machine that answers for the shared names goes first
	// and can mark the build bad if it won't start.
	updateStagger = 30 * time.Minute
	// updateHealthy is how long a new build must run before it is kept.
	updateHealthy = 2 * time.Minute
)

// applyUpdateTimes reads $JAGGERY_UPDATE_TIMES, "first,interval,stagger,
// healthy" as Go durations, so the test house can run a rollout in seconds
// rather than an hour. Nothing else should set it.
func applyUpdateTimes(s string) error {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return fmt.Errorf("JAGGERY_UPDATE_TIMES wants first,interval,stagger,healthy, got %q", s)
	}
	var d [4]time.Duration
	for i, p := range parts {
		v, err := time.ParseDuration(strings.TrimSpace(p))
		if err != nil || v <= 0 {
			return fmt.Errorf("JAGGERY_UPDATE_TIMES: %q isn't a duration", p)
		}
		d[i] = v
	}
	updateFirst, updateInterval, updateStagger, updateHealthy = d[0], d[1], d[2], d[3]
	fmt.Printf("update timings from JAGGERY_UPDATE_TIMES (for tests): first %s, every %s, stagger %s, healthy after %s\n", d[0], d[1], d[2], d[3])
	return nil
}

// rollbackSelf puts the previous build back; tests replace it so they don't
// swap out the test binary.
var rollbackSelf = func() error {
	exe, err := executable()
	if err != nil {
		return err
	}
	return update.Rollback(exe)
}

// agentStarted is when this agent started on its current build.
var agentStarted time.Time

// lastUpdate is the agent's latest update check, shared in its inventory.
var lastUpdate atomic.Pointer[update.Status]

// checkPendingUpdate runs at agent start. If the previous run restarted into
// a new build that keeps failing to start, put the old one back, remember
// the build as bad, and return errRestart to run the old one.
func checkPendingUpdate(dir string) error {
	p, ok := update.LoadPending(dir)
	if !ok {
		return nil
	}
	if version.Commit == p.From {
		update.ClearPending(dir) // back on the build we came from
		return nil
	}
	p.Attempts++
	if p.Attempts <= update.MaxAttempts {
		return update.SavePending(dir, p)
	}

	fmt.Printf("this build failed to start %d times; going back to the previous one\n", update.MaxAttempts)
	if err := update.MarkBad(dir, version.Commit); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record the bad build:", err)
	}
	update.ClearPending(dir)
	if err := rollbackSelf(); err != nil {
		return fmt.Errorf("rolling back a build that won't start: %w", err)
	}
	return errRestart
}

// keepUpdate clears the pending marker once a new build has run long enough.
func keepUpdate(ctx context.Context, dir string) {
	if _, ok := update.LoadPending(dir); !ok {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(updateHealthy):
		update.ClearPending(dir)
		fmt.Printf("keeping %s: it has run for %s\n", version.String(), updateHealthy)
	}
}

// watchUpdates checks the release channel on a schedule and acts on the
// machine's policy: install (staggered), only report, or nothing.
func watchUpdates(ctx context.Context, id *identity.Identity, store *home.Store, dir string) {
	timer := time.NewTimer(updateFirst)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(updateInterval)

		st := checkForUpdate(ctx, id, store, dir, loadHouse(), time.Now())
		lastUpdate.Store(&st)
	}
}

func checkForUpdate(ctx context.Context, id *identity.Identity, store *home.Store, dir string, snap house.Snapshot, now time.Time) update.Status {
	s := update.LoadSettings(dir, version.Channel)
	st := update.Status{Policy: s.Policy, Channel: s.Channel, CheckedAt: now.UTC()}
	if s.Policy == update.Off {
		return st
	}

	m, fetch, via, err := findRelease(ctx, id, store, dir, snap, s.Channel)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	snap = withOwnWithdrawals(snap, update.Withdrawn(dir))
	run := update.RunningNow()
	leaving := inHouse(snap, run.Commit, func(inv inventory.Report) []string { return inv.WithdrawnCommits })
	if !update.Newer(m, run) && !leaving {
		return st
	}
	st.Available, st.Via = m.Version, via
	if s.Policy != update.Auto && !leaving {
		return st
	}

	// Installed already and restarting: a second install would make the new
	// build its own previous one, and leave nothing to roll back to (found
	// by the test house).
	if v, _ := installedAwaitingRestart.Load().(string); v != "" {
		st.Waiting = "installed " + v + "; restarting into it"
		return st
	}
	// This machine's own rollbacks count at once, before its next scan puts
	// them in the house's view.
	if slices.Contains(update.Bad(dir), m.Commit) {
		st.Waiting = "this machine rolled back from " + m.Version + ", so it is skipped"
		return st
	}

	p := planUpdate(id.ID, snap, m, run, s.Channel, now)
	if p.Version == "" {
		st.Waiting = p.Wait
		return st
	}
	if p.Version != m.Version {
		// The build the first machine proved, not the newest.
		m, fetch, via, err = findReleaseVersion(ctx, id, store, dir, snap, s.Channel, p.Version)
		if err != nil {
			st.Error = err.Error()
			return st
		}
	}

	exe, err := executable()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	data, err := fetch(ctx)
	if err == nil {
		err = update.Install(exe, data)
	}
	if err != nil {
		st.Error = err.Error()
		return st
	}
	from := "GitHub"
	if via != "" {
		from = via
	}
	installedAwaitingRestart.Store(m.Version)
	fmt.Printf("installed %s from %s (signature and checksum verified); restarting into it\n", m.Version, from)
	return st
}

// installedAwaitingRestart is the version this agent installed and is about
// to restart into; nothing more is installed until then.
var installedAwaitingRestart atomic.Value

// inHouse reports whether any machine's list (from its inventory) has commit.
// withOwnWithdrawals adds the withdrawals this machine has just recorded
// (findRelease marks them) to its own entry in the house view, so they count
// at once rather than after the next scan, as its own rollbacks do. Found by
// the first withdrawal rehearsal: a machine running a withdrawn build stayed
// on it for a whole update interval.
func withOwnWithdrawals(snap house.Snapshot, own []string) house.Snapshot {
	if len(own) == 0 {
		return snap
	}
	snap.Self.Inventory.WithdrawnCommits = append(slices.Clone(snap.Self.Inventory.WithdrawnCommits), own...)
	return snap
}

func inHouse(snap house.Snapshot, commit string, list func(inventory.Report) []string) bool {
	if commit == "" {
		return false
	}
	for _, m := range snap.All() {
		for _, c := range list(m.Inventory) {
			if c == commit {
				return true
			}
		}
	}
	return false
}

// findReleaseVersion fetches a specific version: from GitHub by its tag
// (keeping it for the house, next to the newest), or else from a relay.
func findReleaseVersion(ctx context.Context, id *identity.Identity, store *home.Store, dir string, snap house.Snapshot, channel, want string) (update.Manifest, func(context.Context) ([]byte, error), string, error) {
	src := update.NewSource(update.Token(dir))
	var ghErr error
	for _, tag := range update.TagsFor(want) {
		rel, err := src.ReleaseByTag(ctx, tag)
		if err != nil {
			ghErr = err
			continue
		}
		if err := cacheForHouse(ctx, src, rel, snap, dir); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not keep builds for the house:", err)
		}
		return rel.Manifest, func(ctx context.Context) ([]byte, error) {
			return src.Download(ctx, rel, version.Asset())
		}, "", nil
	}
	r, peer, err := peerRelease(ctx, id, store, channel, want)
	if err != nil {
		return update.Manifest{}, nil, "", fmt.Errorf("%s: %v; %v", want, ghErr, err)
	}
	return r.Manifest, func(ctx context.Context) ([]byte, error) {
		return peerDownload(ctx, id, peer, r, version.Asset())
	}, peer.Name, nil
}

// findRelease looks for the newest release on GitHub, and keeps its builds
// for the rest of the house when that works. Otherwise (no token, no
// internet) it takes the newest one a directly approved peer relays. It
// returns how to fetch this machine's build, and the relaying peer's name.
func findRelease(ctx context.Context, id *identity.Identity, store *home.Store, dir string, snap house.Snapshot, channel string) (update.Manifest, func(context.Context) ([]byte, error), string, error) {
	src := update.NewSource(update.Token(dir))
	rel, withdrawn, err := src.LatestAndWithdrawn(ctx, channel)
	for _, w := range withdrawn {
		fmt.Printf("release %s was withdrawn: %s\n", w.Version, w.Withdrawn)
		_ = update.MarkWithdrawn(dir, w.Commit)
	}
	if update.IsSignatureError(err) {
		fmt.Printf("refused the newest release on GitHub: %v\n", err)
	}
	if err == nil {
		if err := cacheForHouse(ctx, src, rel, snap, dir); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not keep builds for the house:", err)
		}
		return rel.Manifest, func(ctx context.Context) ([]byte, error) {
			return src.Download(ctx, rel, version.Asset())
		}, "", nil
	}

	r, peer, perr := peerRelease(ctx, id, store, channel, "")
	if perr != nil {
		return update.Manifest{}, nil, "", fmt.Errorf("%v; %v", err, perr)
	}
	return r.Manifest, func(ctx context.Context) ([]byte, error) {
		return peerDownload(ctx, id, peer, r, version.Asset())
	}, peer.Name, nil
}

// updatePlan is what a machine should install now: Version, or nothing and
// Wait says why.
type updatePlan struct {
	Version string
	Wait    string
}

// planUpdate decides between the newest release and the one the first
// machine (the name holder) has proven. The holder installs the
// newest at once. The others install the holder's build once it has run for
// updateStagger without a failed start, so a stream of merges can't keep
// them waiting forever: they follow the proof, not the newest release. If
// the holder can't prove anything (offline, older than R1, or not updating
// itself), they fall back to waiting updateStagger after the newest appeared.
// A build any machine rolled back from is never installed.
//
// Two rules sit on top (P0c). A build that was withdrawn is never installed,
// and a machine running one moves to the newest good release at once, even
// an older one. On prod, updates wait out a cool-down after publication (a
// day for a patch, three for a minor from 1.0); minor updates before 1.0
// and every major one are left for a person to install.
func planUpdate(self string, snap house.Snapshot, newest update.Manifest, run update.Running, channel string, now time.Time) updatePlan {
	withdrawn := func(commit string) bool {
		return inHouse(snap, commit, func(inv inventory.Report) []string { return inv.WithdrawnCommits })
	}
	bad := func(commit string) (string, bool) {
		for _, machine := range snap.All() {
			for _, b := range machine.Inventory.BadCommits {
				if b == commit {
					return machine.Name, true
				}
			}
		}
		if withdrawn(commit) {
			return "the release channel", true
		}
		return "", false
	}
	if withdrawn(run.Commit) && newest.Commit != run.Commit && !withdrawn(newest.Commit) {
		return updatePlan{Version: newest.Version}
	}
	if who, ok := bad(newest.Commit); ok {
		return updatePlan{Wait: fmt.Sprintf("%s pulled %s, so it is skipped", who, newest.Version)}
	}
	if channel == "prod" && version.Valid(newest.Version) && version.Valid(run.Version) {
		var cooldown time.Duration
		switch change := version.Change(run.Version, newest.Version); {
		case change == "major":
			return updatePlan{Wait: fmt.Sprintf("%s is a major update, which waits for you: run `jag update`", newest.Version)}
		case change == "minor" && version.Compare(run.Version, "1.0.0") < 0:
			return updatePlan{Wait: fmt.Sprintf("before 1.0, minor updates like %s are installed by hand: run `jag update`", newest.Version)}
		case change == "minor":
			cooldown = 72 * time.Hour
		default:
			cooldown = 24 * time.Hour
		}
		if ready := newest.BuiltAt.Add(cooldown); now.Before(ready) {
			return updatePlan{Wait: fmt.Sprintf("letting %s settle in other houses first; installing after %s", newest.Version, ready.Local().Format("Mon 15:04"))}
		}
	}
	// One trial at a time. If another machine is running this release on
	// trial, or went quiet while taking it, let that play out instead of
	// starting a second trial: a machine that crashes or hangs on a new build
	// stops answering, and then the name passes to another machine, which
	// would otherwise install the same build at once (found by the test
	// house). A person can still install it with `jag update`.
	for _, m := range snap.All() {
		if m.MachineID == self {
			continue
		}
		if who, why := trialElsewhere(m, newest, now); who != "" {
			return updatePlan{Wait: fmt.Sprintf("%s %s; waiting for it (jag update installs it anyway)", who, why)}
		}
	}

	holderID := snap.NameHolder()
	if holderID == self {
		return updatePlan{Version: newest.Version}
	}

	var holder house.Machine
	for _, m := range snap.All() {
		if m.MachineID == holderID {
			holder = m
		}
	}
	inv := holder.Inventory
	canProve := holder.Reachable && version.Valid(inv.Build) && !inv.RunningSince.IsZero() &&
		inv.Update != nil && inv.Update.Policy == update.Auto
	if !canProve {
		if ready := newest.BuiltAt.Add(updateStagger); now.Before(ready) {
			return updatePlan{Wait: fmt.Sprintf("letting the first machine try it; installing after %s", ready.Local().Format("15:04"))}
		}
		return updatePlan{Version: newest.Version}
	}

	proven := !inv.UpdatePending && now.Sub(inv.RunningSince) >= updateStagger
	ahead := update.Newer(update.Manifest{Version: inv.Build, Commit: inv.Commit, BuiltAt: inv.RunningSince}, run)
	if _, isBad := bad(inv.Commit); proven && ahead && !isBad {
		return updatePlan{Version: inv.Build}
	}
	if !ahead || !proven {
		since := inv.RunningSince.Add(updateStagger)
		return updatePlan{Wait: fmt.Sprintf("waiting for %s to prove %s first (it runs %s; proven after %s)",
			holder.Name, newest.Version, inv.Build, since.Local().Format("15:04"))}
	}
	return updatePlan{Wait: fmt.Sprintf("%s's build was rolled back elsewhere; waiting for a newer one", holder.Name)}
}

// quietSlack widens "seen after the release came out": a machine that takes
// a release at once restarts before its peers' next scan, so the last time
// they saw it can fall just before the release's time (found by the test
// house's second runs).
const quietSlack = 5 * time.Minute

// trialElsewhere says whether machine m is trying release newest, or may
// be: it was seen after the release came out and has gone quiet since. A
// machine that installs a build restarts into it before anyone hears that
// it took it, so going quiet is the only sign a crash-looping or hung
// trial gives. The wait for a quiet machine ends updateStagger after it
// was last seen, as the fallback for an offline name holder does.
// Laptops are left out: they sleep, and they never go first (C2).
func trialElsewhere(m house.Machine, newest update.Manifest, now time.Time) (who, why string) {
	inv := m.Inventory
	if inv.Update == nil || inv.Update.Policy != update.Auto {
		return "", ""
	}
	onIt := inv.Commit == newest.Commit
	switch {
	case m.Reachable && onIt && inv.UpdatePending:
		return m.Name, "is trying " + newest.Version + " first"
	case !m.Reachable && !inv.Laptop && m.InventoryAt.After(newest.BuiltAt.Add(-quietSlack)) &&
		(!onIt || inv.UpdatePending) && now.Sub(m.InventoryAt) < updateStagger:
		return m.Name, "went quiet after " + newest.Version + " came out"
	}
	return "", ""
}

// printUpdate is jag status's line about updates, from the running agent.
func printUpdate(st *update.Status) {
	switch {
	case st == nil:
		fmt.Printf("updates      not checked yet (the agent checks 2 minutes after it starts)\n")
	case st.Policy == update.Off:
		fmt.Printf("updates      off (jag update --policy auto to turn on)\n")
	case st.Error != "":
		fmt.Printf("updates      %s on %s; last check failed: %s\n", st.Policy, st.Channel, st.Error)
	case st.Waiting != "":
		fmt.Printf("updates      %s available: %s\n", st.Available, st.Waiting)
	case st.Available != "" && st.Via != "":
		fmt.Printf("updates      %s available from %s: installing, or run jag update\n", st.Available, st.Via)
	case st.Available != "":
		fmt.Printf("updates      %s available: run jag update\n", st.Available)
	default:
		fmt.Printf("updates      up to date (%s on %s, checked %s)\n", st.Policy, st.Channel, st.CheckedAt.Local().Format("15:04"))
	}
}
