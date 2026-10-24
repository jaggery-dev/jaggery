package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/update"
	"github.com/jaggery-dev/jaggery/internal/version"
)

// A house of two: "aaa" (lower id, so it answers for the shared names) and
// "bbb", seen from "bbb". The holder runs build, since startedAgo.
func houseWithHolder(build, commit string, startedAgo time.Duration, pending bool, now time.Time, bad ...string) house.Snapshot {
	return house.Snapshot{
		Self: house.Machine{MachineID: "bbb", Name: "laptop", Self: true, Reachable: true},
		Peers: []house.Machine{{MachineID: "aaa", Name: "pi", Trust: home.Approved, Reachable: true,
			Inventory: inventory.Report{Build: build, Commit: commit, RunningSince: now.Add(-startedAgo),
				UpdatePending: pending, BadCommits: bad, Update: &update.Status{Policy: update.Auto}}}},
	}
}

func TestPlanUpdate(t *testing.T) {
	now := time.Date(2026, 10, 7, 17, 0, 0, 0, time.UTC)
	run := update.Running{Version: "0.5.0-beta.16", Commit: "c16", BuiltAt: now.Add(-3 * time.Hour)}
	newest := update.Manifest{Version: "0.5.0-beta.20", Commit: "c20", BuiltAt: now.Add(-5 * time.Minute)}
	proven := 31 * time.Minute

	cases := []struct {
		name    string
		self    string
		snap    house.Snapshot
		newest  update.Manifest
		install string // "" means wait
	}{
		{"the first machine takes the newest at once", "aaa", houseWithHolder("0.5.0-beta.16", "c16", time.Hour, false, now), newest, "0.5.0-beta.20"},
		{"the others take what it proved", "bbb", houseWithHolder("0.5.0-beta.20", "c20", proven, false, now), newest, "0.5.0-beta.20"},
		// Merges keep coming, the holder proved an
		// older one; take that instead of waiting forever.
		{"merges keep coming", "bbb", houseWithHolder("0.5.0-beta.18", "c18", proven, false, now), newest, "0.5.0-beta.18"},
		{"still on trial", "bbb", houseWithHolder("0.5.0-beta.20", "c20", proven, true, now), newest, ""},
		{"not long enough yet", "bbb", houseWithHolder("0.5.0-beta.20", "c20", 10*time.Minute, false, now), newest, ""},
		{"holder no further than us", "bbb", houseWithHolder("0.5.0-beta.16", "c16", 2*time.Hour, false, now), newest, ""},
		{"the newest was rolled back somewhere", "bbb", houseWithHolder("0.5.0-beta.18", "c18", proven, false, now, "c20"), newest, ""},
		{"the holder's build was rolled back somewhere", "bbb", houseWithHolder("0.5.0-beta.18", "c18", proven, false, now, "c18"), newest, ""},
		// A holder older than R1 can't prove anything: the old rule.
		{"holder can't prove, too soon", "bbb", houseWithHolder("", "c11", 0, false, now), newest, ""},
		{"holder can't prove, later", "bbb", houseWithHolder("", "c11", 0, false, now),
			update.Manifest{Version: "0.5.0-beta.20", Commit: "c20", BuiltAt: now.Add(-time.Hour)}, "0.5.0-beta.20"},
	}
	for _, c := range cases {
		p := planUpdate(c.self, c.snap, c.newest, run, "beta", now)
		if p.Version != c.install {
			t.Errorf("%s: install %q (wait %q), want %q", c.name, p.Version, p.Wait, c.install)
		}
		if p.Version == "" && p.Wait == "" {
			t.Errorf("%s: waiting without saying why", c.name)
		}
	}
}

func TestPlanUpdateFallsBackWhenTheHolderIsOffline(t *testing.T) {
	now := time.Now()
	snap := houseWithHolder("0.5.0-beta.20", "c20", time.Hour, false, now)
	snap.Peers[0].Reachable = false // the Pi is off: the laptop elects... itself
	snap.Self.Inventory.Laptop = true
	newest := update.Manifest{Version: "0.5.0-beta.20", Commit: "c20", BuiltAt: now.Add(-time.Hour)}
	run := update.Running{Version: "0.5.0-beta.16", Commit: "c16", BuiltAt: now.Add(-3 * time.Hour)}
	if p := planUpdate("bbb", snap, newest, run, "beta", now); p.Version != "0.5.0-beta.20" {
		t.Fatalf("got %+v, want the newest", p)
	}
}

// A new build that keeps failing to start is rolled back on its fourth start,
// and remembered as bad.
func TestBuildThatWontStartIsRolledBack(t *testing.T) {
	dir := t.TempDir()
	saved, savedRollback := version.Commit, rollbackSelf
	defer func() { version.Commit, rollbackSelf = saved, savedRollback }()
	version.Commit = "new"
	rolledBack := false
	rollbackSelf = func() error { rolledBack = true; return nil }

	if err := update.SavePending(dir, update.Pending{From: "old"}); err != nil {
		t.Fatal(err)
	}
	for start := 1; start <= update.MaxAttempts; start++ {
		if err := checkPendingUpdate(dir); err != nil {
			t.Fatalf("start %d: %v", start, err)
		}
	}
	if rolledBack {
		t.Fatal("rolled back before the attempts ran out")
	}
	if err := checkPendingUpdate(dir); !errors.Is(err, errRestart) || !rolledBack {
		t.Fatalf("start %d: got %v (rolled back %v), want a rollback and restart", update.MaxAttempts+1, err, rolledBack)
	}
	if bad := update.Bad(dir); len(bad) != 1 || bad[0] != "new" {
		t.Fatalf("bad builds = %v, want [new]", bad)
	}
	if _, ok := update.LoadPending(dir); ok {
		t.Fatal("the marker survived the rollback")
	}
}

// Coming back up on the build we updated from (a manual rollback) is not a
// failed start.
func TestStartingOnThePreviousBuildClearsTheMarker(t *testing.T) {
	dir := t.TempDir()
	saved := version.Commit
	defer func() { version.Commit = saved }()
	version.Commit = "old"
	if err := update.SavePending(dir, update.Pending{From: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := checkPendingUpdate(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := update.LoadPending(dir); ok {
		t.Fatal("the marker was kept")
	}
}

func TestWithdrawnBuilds(t *testing.T) {
	now := time.Date(2026, 10, 7, 17, 0, 0, 0, time.UTC)
	run := update.Running{Version: "0.5.0-beta.20", Commit: "c20", BuiltAt: now.Add(-3 * time.Hour)}
	older := update.Manifest{Version: "0.5.0-beta.19", Commit: "c19", BuiltAt: now.Add(-4 * time.Hour)}

	// The build this machine runs was withdrawn: move to the newest good
	// release at once, even though it's older, and even if not first.
	snap := houseWithHolder("0.5.0-beta.20", "c20", time.Hour, false, now)
	snap.Peers[0].Inventory.WithdrawnCommits = []string{"c20"}
	if p := planUpdate("bbb", snap, older, run, "beta", now); p.Version != "0.5.0-beta.19" {
		t.Fatalf("running a withdrawn build: %+v, want to move to beta.19", p)
	}

	// A withdrawn newest is never installed.
	newest := update.Manifest{Version: "0.5.0-beta.21", Commit: "c21", BuiltAt: now.Add(-time.Hour)}
	snap.Peers[0].Inventory.WithdrawnCommits = []string{"c21"}
	if p := planUpdate("aaa", snap, newest, run, "beta", now); p.Version != "" {
		t.Fatalf("installed a withdrawn release: %+v", p)
	}
}

func TestProdCoolDownsAndConsent(t *testing.T) {
	now := time.Date(2026, 10, 7, 17, 0, 0, 0, time.UTC)
	self := house.Snapshot{Self: house.Machine{MachineID: "aaa", Self: true, Reachable: true}} // first machine
	at := func(v string, age time.Duration) update.Manifest {
		return update.Manifest{Version: v, Commit: "c-" + v, BuiltAt: now.Add(-age)}
	}
	run := func(v string) update.Running {
		return update.Running{Version: v, Commit: "run", BuiltAt: now.Add(-30 * 24 * time.Hour)}
	}
	cases := []struct {
		name    string
		running string
		m       update.Manifest
		install bool
	}{
		{"fresh patch waits a day", "0.5.0", at("0.5.1", 2*time.Hour), false},
		{"day-old patch installs", "0.5.0", at("0.5.1", 25*time.Hour), true},
		{"minor before 1.0 is by hand", "0.5.1", at("0.6.0", 10*24*time.Hour), false},
		{"minor after 1.0 waits three days", "1.0.0", at("1.1.0", 48*time.Hour), false},
		{"minor after 1.0, after three days", "1.0.0", at("1.1.0", 73*time.Hour), true},
		{"major always waits for consent", "1.4.0", at("2.0.0", 30*24*time.Hour), false},
	}
	for _, c := range cases {
		p := planUpdate("aaa", self, c.m, run(c.running), "prod", now)
		if (p.Version != "") != c.install {
			t.Errorf("%s: %+v", c.name, p)
		}
	}
	// Beta has no cool-down.
	if p := planUpdate("aaa", self, at("0.5.0-beta.30", time.Minute), run("0.5.0-beta.29"), "beta", now); p.Version == "" {
		t.Errorf("beta waited: %+v", p)
	}
}

// Found by the test house (Q3): the name holder crash-loops on a new build,
// stops answering, and the name passes to another machine, which must not
// start a second trial of the same build.
func TestPlanUpdateWaitsForATrialElsewhere(t *testing.T) {
	now := time.Now()
	newest := update.Manifest{Version: "0.5.0-beta.20", Commit: "c20", BuiltAt: now.Add(-time.Hour)}
	run := update.Running{Version: "0.5.0-beta.16", Commit: "c16", BuiltAt: now.Add(-3 * time.Hour)}

	// The Pi saw the release and went quiet; the laptop now holds the names.
	snap := houseWithHolder("0.5.0-beta.16", "c16", 2*time.Hour, false, now)
	pi := &snap.Peers[0]
	pi.Reachable = false
	pi.InventoryAt = now.Add(-2 * time.Minute)
	if p := planUpdate("bbb", snap, newest, run, "beta", now); p.Version != "" || !strings.Contains(p.Wait, "went quiet") {
		t.Fatalf("got %+v, want a wait for the Pi", p)
	}

	// Still quiet a stagger later: it's offline, not on trial.
	if p := planUpdate("bbb", snap, newest, run, "beta", pi.InventoryAt.Add(updateStagger+time.Minute)); p.Version != "0.5.0-beta.20" {
		t.Fatalf("got %+v, want the newest once the wait is over", p)
	}

	// A laptop going quiet is a laptop asleep.
	pi.Inventory.Laptop = true
	if p := planUpdate("bbb", snap, newest, run, "beta", now); strings.Contains(p.Wait, "went quiet") {
		t.Fatalf("a sleeping laptop held the house back: %+v", p)
	}
	pi.Inventory.Laptop = false

	// Last seen just before the release: it may have taken it at once and
	// restarted before anyone scanned it again.
	pi.InventoryAt = newest.BuiltAt.Add(-20 * time.Second)
	if p := planUpdate("bbb", snap, newest, run, "beta", now.Add(-50*time.Minute)); p.Version != "" {
		t.Fatalf("got %+v, want a wait for the Pi seen just before the release", p)
	}

	// The same, but the Pi went off well before the release came out: an
	// offline machine isn't a trial, and the old fallback applies.
	pi.InventoryAt = newest.BuiltAt.Add(-time.Hour)
	if p := planUpdate("bbb", snap, newest, run, "beta", now); p.Version != "0.5.0-beta.20" {
		t.Fatalf("got %+v, want the newest", p)
	}

	// Another machine is running it on trial, reachable: wait for it too.
	snap = houseWithHolder("0.5.0-beta.20", "c20", time.Minute, true, now)
	if p := planUpdate("bbb", snap, newest, run, "beta", now); p.Version != "" || !strings.Contains(p.Wait, "trying") {
		t.Fatalf("got %+v, want a wait for the trial", p)
	}
}

// A withdrawal this machine has only just recorded counts at once: nothing
// in the house view says so yet (the first withdrawal rehearsal stayed on the
// withdrawn build for a whole update interval).
func TestOwnWithdrawalCountsAtOnce(t *testing.T) {
	now := time.Date(2026, 10, 24, 15, 13, 0, 0, time.UTC)
	run := update.Running{Version: "0.7.0-beta.4", Commit: "c4", BuiltAt: now.Add(-time.Hour)}
	older := update.Manifest{Version: "0.7.0-beta.3", Commit: "c3", BuiltAt: now.Add(-2 * time.Hour)}
	snap := houseWithHolder("0.7.0-beta.4", "c4", time.Hour, false, now)

	if inHouse(snap, run.Commit, func(inv inventory.Report) []string { return inv.WithdrawnCommits }) {
		t.Fatal("test set-up: the house view already knows")
	}
	snap = withOwnWithdrawals(snap, []string{"c4"})
	if !inHouse(snap, run.Commit, func(inv inventory.Report) []string { return inv.WithdrawnCommits }) {
		t.Fatal("this machine's own withdrawal isn't counted")
	}
	if p := planUpdate("bbb", snap, older, run, "beta", now); p.Version != "0.7.0-beta.3" {
		t.Fatalf("running a build it has just seen withdrawn: %+v, want to move to beta.3", p)
	}
	if got := withOwnWithdrawals(snap, nil); len(got.Self.Inventory.WithdrawnCommits) != 1 {
		t.Fatal("no own withdrawals should change nothing")
	}
}
