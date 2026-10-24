package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/netif"
)

// jag doctor checks, in order, each thing that has broken a
// house so far, and says how to fix what fails. It runs beside the
// agent: what only the agent knows (dial-back, exposure, updates) comes from
// the house snapshot it writes.

type checkState int

const (
	pass checkState = iota
	warn
	fail
)

type checkResult struct {
	name   string
	state  checkState
	detail string
	fix    string
}

func doctor() error {
	var results []checkResult
	add := func(r checkResult) {
		results = append(results, r)
		if jsonOutput {
			return
		}
		mark := map[checkState]string{pass: "✓", warn: "!", fail: "✗"}[r.state]
		fmt.Printf("%s %-14s %s\n", mark, r.name, r.detail)
		if r.fix != "" && r.state != pass {
			fmt.Printf("  %-14s fix: %s\n", "", r.fix)
		}
	}

	id, err := identity.Load()
	if err != nil {
		add(checkResult{"identity", fail, "none on this machine", "run `jag up` (or install the service: scripts/install.sh)"})
		return doctorResult(results)
	}
	add(checkResult{"identity", pass, fmt.Sprintf("%s (%s)", id.Name, id.ID), ""})

	if agentRunning() {
		add(checkResult{"agent", pass, "running here", ""})
	} else {
		add(checkResult{"agent", fail, "not running on this machine", "start it: `jag up`, or `sudo systemctl start jaggery`"})
	}

	usable, skipped, _ := netif.Usable()
	if len(usable) == 0 {
		add(checkResult{"network", fail, fmt.Sprintf("no usable interface (%d skipped)", len(skipped)), "connect this machine to the home network (Wi-Fi or Ethernet); VPN and container interfaces don't count"})
	} else {
		add(checkResult{"network", pass, fmt.Sprintf("%s %s", usable[0].Name, joinAddrs(usable[0].Addrs)), ""})
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	add(checkMulticast(id, store, usable))
	for _, name := range []string{"jaggery.local", "ai.local"} {
		add(checkResolves(name))
	}
	add(checkWeb())
	add(checkNameClaims(loadHouse()))

	snap := loadHouse()
	add(checkInbound(snap))
	add(checkExposure(snap))
	add(checkClock(id, store))
	add(checkUpdates(snap))

	return doctorResult(results)
}

// doctorJSON is one check in `jag doctor --json`.
type doctorJSON struct {
	Name   string `json:"name"`
	State  string `json:"state"` // pass, warn or fail
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorResult is how doctor ends: exit 0 when every check passed, 4 when
// some only warned, 5 when any failed.
func doctorResult(results []checkResult) error {
	if jsonOutput {
		return doctorResultJSON(results)
	}
	warned := 0
	for _, r := range results {
		if r.state == fail {
			return exitError{exitDoctorFail, errDoctor}
		}
		if r.state == warn {
			warned++
		}
	}
	if warned > 0 {
		fmt.Printf("\nnothing failed; %d warning(s) (see above)\n", warned)
		return exitError{code: exitDoctorWarn}
	}
	fmt.Println("\neverything checked out")
	return nil
}

var errDoctor = fmt.Errorf("some checks failed (see above)")

func doctorResultJSON(results []checkResult) error {
	checks := make([]doctorJSON, 0, len(results))
	code := exitOK
	for _, r := range results {
		c := doctorJSON{Name: r.name, State: [...]string{pass: "pass", warn: "warn", fail: "fail"}[r.state], Detail: r.detail}
		if r.state != pass {
			c.Fix = r.fix
		}
		switch {
		case r.state == fail:
			code = exitDoctorFail
		case r.state == warn && code == exitOK:
			code = exitDoctorWarn
		}
		checks = append(checks, c)
	}
	data := map[string]any{"checks": checks}
	if code == exitDoctorFail {
		emitJSONError(exitError{exitDoctorFail, errors.New("some checks failed")}, data)
	} else if err := emitJSON(data); err != nil {
		return err
	}
	if code == exitOK {
		return nil
	}
	return exitError{code: code}
}

// checkNameClaims reports devices outside the house that answer for the
// shared names: whatever is sent to that name may go to them.
func checkNameClaims(snap house.Snapshot) checkResult {
	if len(snap.NameClaims) == 0 {
		return checkResult{"name claims", pass, "only machines in the house answer for jaggery.local and ai.local", ""}
	}
	var seen []string
	for _, c := range snap.NameClaims {
		seen = append(seen, claimText(c))
	}
	return checkResult{"name claims", fail, strings.Join(seen, "; "),
		"find that device and turn off whatever answers for the name, or take it off the network; until then, open a machine by its own name (http://<machine>.local) for anything private; if it's a Jaggery machine of yours, approve it (jag approve <id>)"}
}

// checkMulticast scans for the house over mDNS and compares with the
// machines approved here: none answering means multicast is blocked.
func checkMulticast(id *identity.Identity, store *home.Store, usable []netif.Interface) checkResult {
	peers, _ := store.List()
	approved := 0
	for _, p := range peers {
		if p.Trust == home.Approved {
			approved++
		}
	}
	if len(usable) == 0 {
		return checkResult{"multicast", fail, "no interface to scan on", ""}
	}
	session, err := discovery.Open(usable)
	if err != nil {
		return checkResult{"multicast", fail, err.Error(), "another program may hold port 5353 exclusively; Jaggery shares it with Avahi and mDNSResponder"}
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	found, _ := session.Find(ctx, id.ID, 3*time.Second)
	switch {
	case approved == 0:
		return checkResult{"multicast", warn, fmt.Sprintf("%d machine(s) seen; none approved here yet", len(found)), "add one: `jag invite` on a machine in the house, `jag join <code>` here"}
	case len(found) == 0:
		return checkResult{"multicast", fail, fmt.Sprintf("no machine answered; %d approved here", approved),
			"the network may block multicast (guest Wi-Fi, \"AP isolation\", some mesh systems): use the main network, or turn that setting off on the router"}
	case len(found) < approved:
		return checkResult{"multicast", warn, fmt.Sprintf("%d of %d approved machines answered", len(found), approved), "the others may be asleep or off"}
	}
	return checkResult{"multicast", pass, fmt.Sprintf("%d of %d approved machines answered", len(found), approved), ""}
}

// checkResolves asks the system resolver, which is what browsers and apps
// use, not Jaggery's own mDNS code.
func checkResolves(name string) checkResult {
	addrs := systemResolve(name)
	if len(addrs) > 0 && runtime.GOOS == "linux" {
		// Browsers ask for IPv4 and IPv6 together. On Linux, Avahi waits
		// out its timeout for an IPv6 answer the alias doesn't have,
		// so time the lookup apps really make.
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_ = exec.CommandContext(ctx, "getent", "hosts", name).Run()
		cancel()
		if took := time.Since(start); took > time.Second {
			return checkResult{name, warn, fmt.Sprintf("%s, but apps wait %.1f s for it (no IPv6 answer)", strings.Join(addrs, ", "), took.Seconds()),
				"known issue on Linux; http://" + name + " still loads, slowly"}
		}
	}
	if len(addrs) == 0 {
		return checkResult{name, fail, "doesn't resolve on this machine",
			"Linux: install and start Avahi with nss-mdns (`mdns4_minimal` in /etc/nsswitch.conf); also check a machine in the house is running the agent"}
	}
	return checkResult{name, pass, strings.Join(addrs, ", "), ""}
}

func checkWeb() checkResult {
	addrs := systemResolve("jaggery.local")
	if len(addrs) == 0 {
		return checkResult{"port 80", fail, "can't try: jaggery.local doesn't resolve", ""}
	}
	// Dial the address the system resolved (see systemResolve), as a
	// browser would, asking for the name.
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(addrs[0], "80"))
		},
	}}
	resp, err := client.Get("http://jaggery.local/")
	if err != nil {
		return checkResult{"port 80", fail, "http://jaggery.local didn't answer: " + shortErr(err),
			"the machine answering for the names needs port 80: install with scripts/install.sh, or free port 80 there"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("Jaggery")) {
		return checkResult{"port 80", fail, fmt.Sprintf("http://jaggery.local answered %s, but not as Jaggery", resp.Status),
			"something else holds port 80 on the machine answering for the names"}
	}
	return checkResult{"port 80", pass, "http://jaggery.local shows the dashboard", ""}
}

func checkInbound(snap house.Snapshot) checkResult {
	r := snap.Self.Inventory.Inbound
	if r == nil {
		return checkResult{"inbound", warn, "not checked yet (needs an approved peer, 20 s after the agent starts)", ""}
	}
	if blocked := r.Blocked(); len(blocked) > 0 {
		var fixes []string
		for _, c := range r.Checks {
			if !c.Open {
				fixes = append(fixes, c.Fix)
			}
		}
		return checkResult{"inbound", fail, fmt.Sprintf("%s can't reach ports %v here", r.CheckedBy, blocked), strings.Join(fixes, "; ")}
	}
	var fixes6 []string
	for _, c := range r.Checks6 {
		if !c.Open && c.Port == discovery.WebPort { // the only port the names need over IPv6
			fixes6 = append(fixes6, c.Fix)
		}
	}
	if len(fixes6) > 0 {
		return checkResult{"inbound", warn, fmt.Sprintf("%s reaches every port over IPv4, not over IPv6, so the shared names stay IPv4-only (5 s lookups on Linux)", r.CheckedBy), strings.Join(fixes6, "; ")}
	}
	return checkResult{"inbound", pass, fmt.Sprintf("%s reaches every port (%s ago)", r.CheckedBy, time.Since(r.CheckedAt).Round(time.Second)), ""}
}

func checkExposure(snap house.Snapshot) checkResult {
	r := snap.Self.Inventory.Inbound
	if r == nil || r.Runtimes == nil {
		return checkResult{"ai runtimes", warn, "exposure not checked yet", ""}
	}
	if exposed := r.Exposed(); len(exposed) > 0 {
		var names, fixes []string
		for _, c := range exposed {
			names = append(names, fmt.Sprintf("%s (port %d)", c.Kind, c.Port))
			fixes = append(fixes, c.Fix)
		}
		return checkResult{"ai runtimes", fail, "open to the whole network: " + strings.Join(names, ", "), strings.Join(fixes, "; ")}
	}
	return checkResult{"ai runtimes", pass, "none open to the network; reached through Jaggery only", ""}
}

// checkClock compares this machine's clock with an approved peer's, from
// the Date header of its secured endpoint. A clock hours off (the Pi's was)
// confuses update times and logs.
func checkClock(id *identity.Identity, store *home.Store) checkResult {
	peers, _ := store.List()
	for _, p := range peers {
		if p.Trust != home.Approved || !p.Verified() || len(p.Addrs) == 0 {
			continue
		}
		client, err := peerHTTPClient(id, ed25519.PublicKey(p.PublicKey), 4*time.Second)
		if err != nil {
			continue
		}
		sent := time.Now()
		resp, err := client.Get("https://" + net.JoinHostPort(p.Addrs[0].String(), strconv.Itoa(discovery.SecurePort)) + "/v1/hello")
		if err != nil {
			continue
		}
		resp.Body.Close()
		theirs, err := http.ParseTime(resp.Header.Get("Date"))
		if err != nil {
			continue
		}
		mid := sent.Add(time.Since(sent) / 2)
		off := mid.Sub(theirs).Round(time.Second)
		if off < 0 {
			off = -off
		}
		if off > time.Minute {
			return checkResult{"clock", fail, fmt.Sprintf("%s off from %s", off, p.Name),
				"turn on network time: `sudo timedatectl set-ntp true` (Linux), or Date & Time settings"}
		}
		return checkResult{"clock", pass, fmt.Sprintf("within %s of %s", max(off, time.Second), p.Name), ""}
	}
	return checkResult{"clock", warn, "no approved peer to compare with", ""}
}

func checkUpdates(snap house.Snapshot) checkResult {
	st := snap.Self.Inventory.Update
	switch {
	case st == nil:
		return checkResult{"updates", warn, "not checked yet (the agent checks 2 minutes after it starts)", ""}
	case st.Error != "":
		return checkResult{"updates", warn, "last check failed: " + st.Error,
			"check this machine can reach api.github.com; if GitHub is refusing (rate limit), one machine with a token (`jag update --help`) can serve the rest"}
	case st.Waiting != "":
		return checkResult{"updates", pass, fmt.Sprintf("%s on %s; %s %s", st.Policy, st.Channel, st.Available, st.Waiting), ""}
	case st.Available != "":
		return checkResult{"updates", warn, fmt.Sprintf("%s available", st.Available), "run `jag update`"}
	}
	return checkResult{"updates", pass, fmt.Sprintf("up to date (%s on %s)", st.Policy, st.Channel), ""}
}

// systemResolve looks a name up the way the rest of the machine does. Go's
// own resolver, which a static build uses, can't resolve .local names (it
// asks DNS), so on Linux and macOS ask the OS's tools instead.
func systemResolve(name string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out []byte
	var err error
	switch runtime.GOOS {
	case "linux":
		// IPv4 only: asking for both stalls on the alias (see checkResolves).
		out, err = exec.CommandContext(ctx, "getent", "ahostsv4", name).Output()
		if err == nil {
			var addrs []string
			seen := map[string]bool{}
			for _, line := range strings.Split(string(out), "\n") {
				if f := strings.Fields(line); len(f) > 0 && !seen[f[0]] {
					seen[f[0]] = true
					addrs = append(addrs, f[0])
				}
			}
			return addrs
		}
	case "darwin":
		out, err = exec.CommandContext(ctx, "dscacheutil", "-q", "host", "-a", "name", name).Output()
		if err == nil {
			var addrs []string
			for _, line := range strings.Split(string(out), "\n") {
				if v, ok := strings.CutPrefix(line, "ip_address: "); ok {
					addrs = append(addrs, strings.TrimSpace(v))
				}
			}
			return addrs
		}
	}
	addrs, _ := net.DefaultResolver.LookupHost(ctx, name)
	return addrs
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// quietLog is the agents' HTTP servers' error log, minus the handshake
// errors a peer's dial-back check (B1) causes on every run by connecting
// and hanging up without TLS: they're the check working, not a fault.
func quietLog() *log.Logger {
	return log.New(quietWriter{os.Stderr}, "", log.LstdFlags)
}

type quietWriter struct{ w io.Writer }

func (q quietWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("TLS handshake error")) && bytes.HasSuffix(bytes.TrimSpace(p), []byte("EOF")) {
		return len(p), nil
	}
	return q.w.Write(p)
}
