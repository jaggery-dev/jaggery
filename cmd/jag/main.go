package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/compat"
	"github.com/jaggery-dev/jaggery/internal/dashboard"
	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/languard"
	"github.com/jaggery-dev/jaggery/internal/licences"
	"github.com/jaggery-dev/jaggery/internal/modelwatch"
	"github.com/jaggery-dev/jaggery/internal/netif"
	"github.com/jaggery-dev/jaggery/internal/pairing"
	"github.com/jaggery-dev/jaggery/internal/reach"
	"github.com/jaggery-dev/jaggery/internal/secure"
	"github.com/jaggery-dev/jaggery/internal/update"
	"github.com/jaggery-dev/jaggery/internal/version"
)

const usage = `jag - every machine in your house, one system

usage:
  jag up              start the agent on this machine
  jag status          show this machine's identity and the peers it can see
  jag peers           list known peers and whether they are approved
  jag map             show every machine in the house and its hardware
  jag invite          print a one-time code for adding a new machine
  jag join <code>     join the house with a code from jag invite (no address needed)
  jag join <code> --at <address>  join through a machine beyond this network (a VPN, another subnet)
  jag join <address>  reach a machine by address, verify it, and trust it
  jag approve <id>    approve a pending peer (id or a unique prefix)
  jag ping <id>       reach an approved peer over mutual TLS
  jag forget <id>     remove a peer from this machine's list
  jag models          every model in the house; approve new ones (jag models approve <name>)
  jag ai pause        send AI requests to other machines for a while (jag ai resume)
  jag doctor          check this machine and its network, and say how to fix what fails
  jag activity [day]  who used which AI model, on which machine (default today)
  jag outbound [day]  what left the house for hosted models (jag outbound show <id>)
  jag apps            the apps on the page; add your own (jag apps add <name> --port <port>)
  jag share           folders this machine lends to models and agents (jag share add <folder>)
  jag files [log]     what models and agents read and changed in shares (jag files restore)
  jag version         show this build's version
  jag licences        the licences of Jaggery and the software built into it
  jag update          install the newest release (--check, --rollback)

jag <command> --help says more about a command; jag help exit-codes lists
what jag's exit codes mean. Commands that only show something take --json.
`

const (
	scanInterval = 10 * time.Second
	scanTimeout  = 2 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(exitUsage)
	}

	if text, ok := helpRequested(os.Args[1:]); ok {
		fmt.Print(text)
		return
	}

	var err error
	args, asJSON := takeJSONFlag(os.Args[2:])
	jsonOutput = asJSON
	if asJSON && !jsonCommands[os.Args[1]] {
		err = errJSONNotHere("jag " + os.Args[1])
	} else {
		err = run(os.Args[1], args)
	}

	// A flag set already printed its usage: asking for help isn't a failure.
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if errors.Is(err, errRestart) {
		err = reexec()
	}
	code, show := exitCode(err)
	switch {
	case show && jsonOutput:
		emitJSONError(err, nil)
	case show:
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(code)
}

// run is one command.
func run(cmd string, args []string) error {
	var err error
	switch cmd {
	case "up":
		err = up()
	case "status":
		err = status()
	case "peers":
		err = listPeers()
	case "map":
		err = showMap()
	case "invite":
		err = invite()
	case "join":
		err = join(args)
	case "approve":
		err = setTrust(args, home.Approved)
	case "ping":
		err = ping(args)
	case "forget":
		err = forget(args)
	case "models":
		err = models(args)
	case "ai":
		err = aiCmd(args)
	case "doctor":
		err = doctor()
	case "activity":
		err = showActivity(args)
	case "outbound":
		err = showOutbound(args)
	case "apps", "app":
		err = appsCmd(args)
	case "share", "shares":
		err = share(args)
	case "files":
		err = files(args)
	case "version", "--version":
		err = showVersion()
	case "licences", "licenses":
		fmt.Print(licences.Text)
	case "update":
		err = selfUpdate(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return exitError{code: exitUsage}
	}
	return err
}

func up() error {
	id, created, err := identity.LoadOrCreate()
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("created this machine's identity: %s (%s)\n", id.Name, id.ID)
	}

	usable, skipped, err := waitForNetwork(netif.Usable, networkWait, os.Stdout)
	if err != nil {
		return err
	}

	stateDir, err := identity.StateDir()
	if err != nil {
		return err
	}
	if err := applyUpdateTimes(os.Getenv("JAGGERY_UPDATE_TIMES")); err != nil {
		return err
	}
	if err := checkPendingUpdate(stateDir); err != nil {
		return err
	}
	crashOnPurpose()
	if err := compat.PrepareState(stateDir, time.Now()); err != nil {
		return err
	}
	agentStarted = time.Now().UTC()
	activityLog, err := activity.Open(stateDir)
	if err != nil {
		return err
	}
	activityLog.Prune(time.Now())
	modelLedger = modelwatch.Open(stateDir)
	approvalList = startApprovals(stateDir, modelLedger)
	toolSeen = openToolLedger(stateDir)
	startHosted(stateDir)

	fmt.Printf("jaggery agent %s running as %s (%s)\n", version.String(), id.Name, id.ID)
	fmt.Printf("fingerprint %s\n\n", id.Fingerprint())
	printInterfaces(usable, skipped)

	store, err := openStore()
	if err != nil {
		return err
	}

	activityRec = newRecorder(activityLog, id.Name, store)
	api, err := serveAgentAPI(id, store, discovery.AgentPort)
	if err != nil {
		return err
	}
	defer api.Close()

	secure, err := serveSecureAPI(id, store, discovery.SecurePort)
	if err != nil {
		return err
	}
	defer secure.Close()

	session, err := discovery.Open(usable)
	if err != nil {
		return err
	}
	defer session.Close()
	session.Announce(id, discovery.AgentPort, usable)
	activityRec.setLookup(session.LookupAddr)
	holdsNames := func(contested bool) bool { return loadHouse().HoldsNames(id.ID, contested) }
	for _, name := range discovery.SharedNames {
		session.AnnounceAlias(name, usable, ipv6ForNames, holdsNames)
	}
	session.WatchSharedNames(usable, func(name string, addr netip.Addr) { nameClaims.add(name, addr, time.Now()) })
	appNamesCtx, stopAppNames := context.WithCancel(context.Background())
	defer stopAppNames()
	go serveAppNames(appNamesCtx, session, usable, holdsNames)

	served := []int{discovery.AgentPort, discovery.SecurePort}
	if err := serveWeb(api, discovery.WebPort); err != nil {
		fmt.Printf("\nport %d     not served: %s\n", discovery.WebPort, webPortHint(err))
	} else {
		fmt.Printf("\nport %d     serving http://jaggery.local and http://ai.local\n", discovery.WebPort)
		servesWeb.Store(true)
		served = append(served, discovery.WebPort)
	}

	fmt.Printf("\nannouncing %s on ports %d (pairing) and %d (secured), looking for peers\n",
		discovery.Service, discovery.AgentPort, discovery.SecurePort)

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, restart := context.WithCancelCause(sigCtx)
	defer restart(nil)

	go watchReach(ctx, id, store, served)
	go watchSelf(ctx, restart)
	go watchUpdates(ctx, id, store, stateDir)
	go keepUpdate(ctx, stateDir)
	go watchModels(ctx, modelLedger)
	watchPeers(ctx, id, session, store)

	if errors.Is(context.Cause(ctx), errRestart) {
		return errRestart // the deferred closes free the ports first
	}
	fmt.Println("\nshutting down")
	return nil
}

// testCrashAfter makes a build that crashes this long after `jag up` starts:
// the test house's broken release, for the rollback check. Set only with
// -ldflags "-X main.testCrashAfter=3s"; releases never set it.
var testCrashAfter string

func crashOnPurpose() {
	d, err := time.ParseDuration(testCrashAfter)
	if testCrashAfter == "" || err != nil {
		return
	}
	fmt.Printf("this is a test build that crashes %s after it starts\n", d)
	go func() {
		time.Sleep(d)
		fmt.Println("test build: crashing on purpose")
		os.Exit(exitCrash)
	}()
}

// networkWait is how often `jag up` looks for a network while it has none.
const networkWait = 5 * time.Second

// waitForNetwork returns once some interface can reach the house. A service
// starts before Wi-Fi joins, and a laptop can be off any network: exiting
// then would leave the service to its restart policy. A
// signal ends the wait.
func waitForNetwork(usable func() ([]netif.Interface, map[string]string, error), every time.Duration, out io.Writer) ([]netif.Interface, map[string]string, error) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	said := false
	for {
		ifaces, skipped, err := usable()
		if err != nil || len(ifaces) > 0 {
			if said && err == nil {
				fmt.Fprintln(out, "network found")
			}
			return ifaces, skipped, err
		}
		if !said {
			fmt.Fprintf(out, "no network yet: waiting for one (checking every %s)\n", every)
			said = true
		}
		select {
		case <-sig:
			return nil, nil, errors.New("stopped while waiting for a network")
		case <-time.After(every):
		}
	}
}

// scanReport decides when a failed peer scan is worth a warning. One failure
// is usually noise: macOS blocks the first send after start until it applies
// the Local Network rule. Two in a row is reported, and so is
// the recovery after a report.
type scanReport struct {
	fails    int
	reported bool
}

// failed returns the warning to print, if any.
func (r *scanReport) failed(err error) string {
	r.fails++
	if r.fails < 2 || r.reported {
		return ""
	}
	r.reported = true
	return fmt.Sprintf("warning: peer scans are failing (%d in a row): %v", r.fails, err)
}

// worked returns the notice to print, if any.
func (r *scanReport) worked() string {
	was := r.reported
	r.fails, r.reported = 0, false
	if was {
		return "peer scans work again"
	}
	return ""
}

// watchPeers rescans periodically, persisting each peer. New peers land as
// pending; the human approves them with `jag approve`. First sight of a peer
// and any change in its trust are printed so a watching operator sees it.
func watchPeers(ctx context.Context, id *identity.Identity, session *discovery.Session, store *home.Store) {
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()

	announced := make(map[string]home.Trust)
	var scans scanReport
	for {
		peers, err := session.Find(ctx, id.ID, scanTimeout)
		switch {
		case err != nil && ctx.Err() == nil:
			if msg := scans.failed(err); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
		case err == nil:
			if msg := scans.worked(); msg != "" {
				fmt.Println(msg)
			}
		}
		for _, peer := range peers {
			if err := store.Observe(observation(peer), time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "warning: could not record peer:", err)
			}
		}

		verifyUnkeyedPeers(ctx, store)
		syncMembers(ctx, id, store)
		syncApprovals(ctx, id, store)
		updateHouse(ctx, id, store)

		// Report first sightings and trust changes, reading trust back from
		// the store so an approval made via the CLI shows up here too.
		if known, err := store.List(); err == nil {
			for _, p := range known {
				if announced[p.MachineID] == p.Trust {
					continue
				}
				announced[p.MachineID] = p.Trust
				fmt.Printf("peer %s: %s (%s)%s\n", p.Trust, p.Name, p.MachineID, vouchedBy(p, known))
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// verifyUnkeyedPeers runs the proof-of-possession exchange against discovered
// peers whose key we don't yet hold, so that trust can be pinned to a key the
// moment a human approves — and so the later mTLS transport has keys to check
// against. Failures are expected (a peer may be briefly unreachable) and quiet.
func verifyUnkeyedPeers(ctx context.Context, store *home.Store) {
	peers, err := store.List()
	if err != nil {
		return
	}
	for _, p := range peers {
		if p.Verified() || len(p.Addrs) == 0 {
			continue
		}
		base := fmt.Sprintf("http://%s", netip.AddrPortFrom(p.Addrs[0], uint16(p.Port)))
		fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		verified, err := pairing.Fetch(fetchCtx, http.DefaultClient, base)
		cancel()
		if err != nil {
			continue
		}
		// The peer must prove the id it advertised, not some other id.
		if verified.MachineID != p.MachineID {
			continue
		}
		_ = store.AttachKey(p.MachineID, verified.PublicKey)
	}
}

// updateHouse assembles the whole-house snapshot — this machine plus every
// approved peer's inventory, fetched over mTLS — and writes it for `jag map`
// and the dashboard to read. A peer we can't currently reach stays in the map
// marked unreachable with its last-known inventory.
func updateHouse(ctx context.Context, id *identity.Identity, store *home.Store) {
	dir, err := identity.StateDir()
	if err != nil {
		return
	}
	prev, _ := house.Load(dir) // keep last-known inventory for unreachable peers

	snap := house.Snapshot{
		Self: house.Machine{
			MachineID:   id.ID,
			Name:        id.Name,
			Self:        true,
			Reachable:   true,
			Fingerprint: id.Fingerprint(),
			Inventory:   collectInventory(),
		},
		UpdatedAt: time.Now().UTC(),
	}
	snap.Self.InventoryAt = snap.Self.Inventory.CollectedAt

	peers, err := store.List()
	if err != nil {
		return
	}
	for _, p := range peers {
		m := house.Machine{
			MachineID: p.MachineID,
			Name:      p.Name,
			Trust:     p.Trust,
			Addrs:     p.Addrs,
		}
		if p.Verified() {
			m.Fingerprint = identity.FingerprintFromKey(ed25519.PublicKey(p.PublicKey))
		}
		if prevMachine, ok := findMachine(prev.Peers, p.MachineID); ok {
			m.Inventory = prevMachine.Inventory
			m.InventoryAt = prevMachine.InventoryAt
		}

		if p.Trust == home.Approved && p.Verified() {
			fetchCtx, cancel := context.WithTimeout(ctx, time.Duration(3*len(p.Addrs)+1)*time.Second)
			report, at, err := fetchInventory(fetchCtx, id, p)
			cancel()
			if err == nil {
				m.Reachable = true
				m.Inventory = report
				m.InventoryAt = time.Now().UTC()
				// Every other call uses the first address: put the one
				// that answered there.
				if at != p.Addrs[0] {
					_ = store.PreferAddr(p.MachineID, at)
				}
			}
		}
		if m.Inventory.OS != "" && !compat.Compatible(m.Inventory.MinProtocol, m.Inventory.Protocol) {
			m.Incompatible = true
		}
		snap.Peers = append(snap.Peers, m)
	}

	snap.Holder = snap.NameHolder()
	snap.NameClaims = nameClaims.foreign(peers, time.Now())
	for _, c := range snap.NameClaims {
		if !claimedBefore(prev.NameClaims, c) {
			fmt.Println("warning: " + claimText(c) + ": it may get what is sent there (jag doctor)")
		}
	}
	_ = house.Save(dir, snap)
}

func findMachine(machines []house.Machine, id string) (house.Machine, bool) {
	for _, m := range machines {
		if m.MachineID == id {
			return m, true
		}
	}
	return house.Machine{}, false
}

func observation(p discovery.Peer) home.Observation {
	return home.Observation{
		MachineID: p.MachineID,
		Name:      p.Name,
		Addrs:     p.Addrs,
		Port:      p.Port,
	}
}

func status() error {
	id, err := identity.Load()
	if err != nil {
		return err
	}

	dir, err := identity.StateDir()
	if err != nil {
		return err
	}

	// The CLI is a separate process from the agent: the dial-back result is
	// only known to the agent, which writes it into the house snapshot.
	report := inventory.Collect()
	self := loadHouse().Self.Inventory
	report.Inbound = self.Inbound
	if !jsonOutput {
		fmt.Printf("name         %s\n", id.Name)
		fmt.Printf("machine id   %s\n", id.ID)
		fmt.Printf("fingerprint  %s\n", id.Fingerprint())
		fmt.Printf("created      %s\n", id.CreatedAt.Format(time.RFC3339))
		fmt.Printf("state        %s\n", dir)
		fmt.Println()
		printInventory(report)
		printUpdate(self.Update)
		fmt.Println()
	}

	usable, skipped, err := netif.Usable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		printInterfaces(usable, skipped)
	}

	store, err := openStore()
	if err != nil {
		return err
	}

	// A live scan refreshes the store so status reflects who is on the
	// network right now, not only who has been seen before.
	if session, err := discovery.Open(usable); err == nil {
		defer session.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*scanTimeout)
		defer cancel()
		if peers, err := session.Find(ctx, id.ID, scanTimeout); err == nil {
			for _, p := range peers {
				_ = store.Observe(observation(p), time.Now())
			}
		}
	}

	if jsonOutput {
		peers, err := peersJSON(store)
		if err != nil {
			return err
		}
		ifaces := []interfaceJSON{}
		for _, i := range usable {
			ifaces = append(ifaces, interfaceJSON{Name: i.Name, Addrs: i.Addrs})
		}
		return emitJSON(statusJSON{
			Name: id.Name, MachineID: id.ID, Fingerprint: id.Fingerprint(), Created: id.CreatedAt,
			StateDir: dir, Inventory: report, Update: self.Update,
			Interfaces: ifaces, Ignored: skipped, Peers: peers,
		})
	}
	fmt.Println()
	return printPeers(store)
}

// statusJSON is `jag status --json`.
type statusJSON struct {
	Name        string            `json:"name"`
	MachineID   string            `json:"machine_id"`
	Fingerprint string            `json:"fingerprint"`
	Created     time.Time         `json:"created"`
	StateDir    string            `json:"state_dir"`
	Inventory   inventory.Report  `json:"inventory"`
	Update      *update.Status    `json:"update,omitempty"`
	Interfaces  []interfaceJSON   `json:"interfaces"`
	Ignored     map[string]string `json:"ignored_interfaces,omitempty"` // name: why
	Peers       []peerJSON        `json:"peers"`
}

type interfaceJSON struct {
	Name  string       `json:"name"`
	Addrs []netip.Addr `json:"addrs"`
}

// peerJSON is a machine this one knows, in `jag peers --json`.
type peerJSON struct {
	MachineID   string       `json:"machine_id"`
	Name        string       `json:"name"`
	Trust       home.Trust   `json:"trust"`
	Verified    bool         `json:"verified"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	Addrs       []netip.Addr `json:"addrs"`
	Port        int          `json:"port"`
	VouchedBy   string       `json:"vouched_by,omitempty"` // the machine whose list approved it
	// SameName: another machine, maybe this one, uses the same name.
	SameName  bool      `json:"same_name,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

func peersJSON(store *home.Store) ([]peerJSON, error) {
	peers, err := store.List()
	if err != nil {
		return nil, err
	}
	self, _ := identity.Load()
	groups := sameNames(self, peers)
	out := make([]peerJSON, 0, len(peers))
	for _, p := range peers {
		j := peerJSON{MachineID: p.MachineID, Name: p.Name, Trust: p.Trust, Verified: p.Verified(),
			Addrs: p.Addrs, Port: p.Port, FirstSeen: p.FirstSeen, LastSeen: p.LastSeen}
		if j.Addrs == nil {
			j.Addrs = []netip.Addr{}
		}
		if p.Verified() {
			j.Fingerprint = identity.FingerprintFromKey(ed25519.PublicKey(p.PublicKey))
		}
		if p.ApprovedVia != "" {
			j.VouchedBy = strings.TrimPrefix(vouchedBy(p, peers), ", vouched for by ")
		}
		j.SameName = sharesName(groups, p.MachineID) != nil
		out = append(out, j)
	}
	return out, nil
}

func printPeers(store *home.Store) error {
	if jsonOutput {
		peers, err := peersJSON(store)
		if err != nil {
			return err
		}
		return emitJSON(map[string]any{"peers": peers})
	}
	peers, err := store.List()
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		fmt.Println("peers        none known yet")
		return nil
	}
	for n, p := range peers {
		label := "peers"
		if n > 0 {
			label = ""
		}
		mark := "unverified"
		if p.Verified() {
			mark = "verified"
		}
		fmt.Printf("%-12s [%-8s %-10s] %s (%s) at %s%s\n", label, p.Trust, mark, p.Name, p.MachineID, endpoints(p), vouchedBy(p, peers))
	}
	self, _ := identity.Load()
	for _, g := range sameNames(self, peers) {
		fmt.Println()
		printSameName(os.Stdout, g)
	}
	return nil
}

// vouchedBy names the machine whose roster approved p, if one did.
func vouchedBy(p home.Peer, peers []home.Peer) string {
	if p.ApprovedVia == "" {
		return ""
	}
	for _, v := range peers {
		if v.MachineID == p.ApprovedVia {
			return ", vouched for by " + v.Name
		}
	}
	return ", vouched for by " + p.ApprovedVia
}

func listPeers() error {
	store, err := openStore()
	if err != nil {
		return err
	}
	return printPeers(store)
}

func setTrust(args []string, trust home.Trust) error {
	if len(args) != 1 {
		return usagef("usage: jag approve <id>")
	}
	store, err := openStore()
	if err != nil {
		return err
	}
	peer, err := store.SetTrust(args[0], trust)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s) is now %s\n", peer.Name, peer.MachineID, trust)
	if peers, err := store.List(); err == nil {
		self, _ := identity.Load()
		if g := sharesName(sameNames(self, peers), peer.MachineID); g != nil {
			fmt.Println()
			printSameName(os.Stdout, g)
		}
	}
	return nil
}

func forget(args []string) error {
	if len(args) != 1 {
		return usagef("usage: jag forget <id>")
	}
	store, err := openStore()
	if err != nil {
		return err
	}
	peer, err := store.Forget(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("forgot %s (%s)\n", peer.Name, peer.MachineID)
	return nil
}

// join reaches a machine at an explicit address — the escape hatch for
// networks where mDNS does not work — proves it holds the key behind the id it
// claims, and records it as approved, pinned to that key.
func join(args []string) error {
	at := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--at" && i+1 < len(args) {
			at = args[i+1]
			args = append(args[:i:i], args[i+2:]...)
			break
		}
	}
	if code, err := pairing.ParseCode(strings.Join(args, " ")); err == nil {
		return joinWithCode(code, at)
	}
	if at != "" {
		return usagef("usage: jag join <code> --at <address>")
	}
	if len(args) != 1 {
		return usagef("usage: jag join <code> or jag join <address>")
	}
	host, port := splitHostPort(args[0])
	base := fmt.Sprintf("http://%s", net.JoinHostPort(host, port))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	verified, err := pairing.Fetch(ctx, http.DefaultClient, base)
	if err != nil {
		return err
	}

	portNum, _ := strconv.Atoi(port)
	obs := home.Observation{MachineID: verified.MachineID, Name: verified.Name, Port: portNum}
	if addr, err := netip.ParseAddr(host); err == nil {
		obs.Addrs = []netip.Addr{addr}
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	if _, err := store.Adopt(obs, verified.PublicKey, home.Approved, time.Now()); err != nil {
		return err
	}

	fmt.Printf("joined %s (%s)\n", verified.Name, verified.MachineID)
	fmt.Printf("fingerprint %s\n", verified.Fingerprint)
	fmt.Println("verify this matches the machine you meant to add, then it is approved.")
	return nil
}

// splitHostPort accepts "host", "host:port", or a bare IPv6 address, filling
// in the default agent port when none is given.
func splitHostPort(addr string) (host, port string) {
	if h, p, err := net.SplitHostPort(addr); err == nil {
		return h, p
	}
	return addr, strconv.Itoa(discovery.AgentPort)
}

// ping reaches an approved peer over mutual TLS and reads its secured
// greeting. It proves the whole trust chain end to end: we must hold the
// peer's key (to pin it) and the peer must have approved us (to let us in).
func ping(args []string) error {
	if len(args) != 1 {
		return usagef("usage: jag ping <id>")
	}
	id, err := identity.Load()
	if err != nil {
		return err
	}
	store, err := openStore()
	if err != nil {
		return err
	}
	peer, err := store.Get(args[0])
	if err != nil {
		return err
	}
	if peer.Trust != home.Approved {
		return fmt.Errorf("%s is %s, not approved — run 'jag approve %s' first", peer.Name, peer.Trust, args[0])
	}
	if !peer.Verified() {
		return fmt.Errorf("%s has no verified key yet", peer.Name)
	}
	if len(peer.Addrs) == 0 {
		return fmt.Errorf("%s has no known address", peer.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, _, err := fetchInventory(ctx, id, peer)
	if err != nil {
		return fmt.Errorf("secured call to %s failed: %w", peer.Name, err)
	}

	fmt.Printf("secured channel to %s (%s) established\n\n", peer.Name, peer.MachineID)
	printInventory(report)
	return nil
}

// fetchInventory opens a mutual-TLS connection to a peer and reads its
// inventory. Both ends authenticate: we pin the peer's key, the peer checks we
// are approved. Shared by `jag ping` and the whole-house snapshot builder.
func fetchInventory(ctx context.Context, id *identity.Identity, peer home.Peer) (inventory.Report, netip.Addr, error) {
	if !peer.Verified() {
		return inventory.Report{}, netip.Addr{}, fmt.Errorf("%s has no verified key", peer.Name)
	}
	if len(peer.Addrs) == 0 {
		return inventory.Report{}, netip.Addr{}, fmt.Errorf("%s has no known address", peer.Name)
	}

	client, err := peerHTTPClient(id, ed25519.PublicKey(peer.PublicKey), 5*time.Second)
	if err != nil {
		return inventory.Report{}, netip.Addr{}, err
	}

	// Try each known address: a machine that moved (a new DHCP lease, Wi-Fi
	// instead of Ethernet) keeps its old address in the list for a while,
	// and one dead address mustn't make it look offline.
	var lastErr error
	for _, a := range peer.Addrs {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		report, err := inventoryAt(attempt, client, a)
		cancel()
		if err == nil {
			return report, a, nil
		}
		lastErr = err
	}
	return inventory.Report{}, netip.Addr{}, lastErr
}

func inventoryAt(ctx context.Context, client *http.Client, a netip.Addr) (inventory.Report, error) {
	addr := net.JoinHostPort(a.String(), strconv.Itoa(discovery.SecurePort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/v1/inventory", nil)
	if err != nil {
		return inventory.Report{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return inventory.Report{}, err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return inventory.Report{}, fmt.Errorf("answered %s", resp.Status)
	}

	var report inventory.Report
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&report); err != nil {
		return inventory.Report{}, fmt.Errorf("decoding inventory: %w", err)
	}
	return report, nil
}

// showMap renders the whole-house snapshot the agent maintains.
func showMap() error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	snap, err := house.Load(dir)
	if err != nil {
		return err
	}
	if jsonOutput {
		// The snapshot the agent writes, with which machine answers for the
		// shared names.
		return emitJSON(map[string]any{"updated_at": snap.UpdatedAt, "name_holder": snap.NameHolder(), "machines": snap.All(), "name_claims": snap.NameClaims})
	}

	fmt.Printf("house as of %s\n", snap.UpdatedAt.Local().Format("15:04:05"))
	holder := snap.NameHolder()
	for _, m := range snap.All() {
		if m.MachineID == holder {
			fmt.Printf("jaggery.local and ai.local answered by %s\n", m.Name)
		}
	}
	fmt.Println()
	for _, m := range snap.All() {
		printMachine(m)
	}
	return nil
}

func printMachine(m house.Machine) {
	tag := string(m.Trust)
	switch {
	case m.Self:
		tag = "this machine"
	case m.Incompatible:
		tag = string(m.Trust) + ", INCOMPATIBLE: update it (jag update)"
	case !m.Reachable:
		tag = string(m.Trust) + ", offline"
	}

	fmt.Printf("%-18s %s (%s)\n", m.Name, "["+tag+"]", m.MachineID)
	inv := m.Inventory
	if inv.OS == "" {
		fmt.Printf("    no inventory yet\n\n")
		return
	}
	fmt.Printf("    role %s · %s/%s · %d CPU · %s RAM",
		inv.Role, inv.OS, inv.Arch, inv.CPUCount, humanBytes(inv.MemoryBytes))
	if inv.DiskTotalBytes > 0 {
		fmt.Printf(" · %s/%s disk free", humanBytes(inv.DiskFreeBytes), humanBytes(inv.DiskTotalBytes))
	}
	for _, g := range inv.GPUs {
		fmt.Printf(" · gpu %s", gpuText(g))
	}
	fmt.Println()
	if p := inv.AIPaused; p.Active(time.Now()) {
		fmt.Printf("    AI requests paused %s\n", pauseEnd(p))
	}
	if inv.Version != "" {
		fmt.Printf("    jag %s\n", inv.Version)
	}
	for _, rt := range inv.Runtimes {
		fmt.Printf("    %s at %s:", rt.Kind, rt.Endpoint)
		for _, m := range rt.Models {
			if m.Loaded {
				fmt.Printf(" %s(loaded)", m.Name)
			} else {
				fmt.Printf(" %s", m.Name)
			}
		}
		fmt.Println()
	}
	fmt.Println()
}

func openStore() (*home.Store, error) {
	dir, err := identity.StateDir()
	if err != nil {
		return nil, err
	}
	return home.Open(dir)
}

// gpuText is a GPU as `jag map` and `jag status` show it: "NVIDIA L4: 21.8
// GiB free of 22.5 GiB". Memory left out is unknown, not zero.
func gpuText(g inventory.GPU) string {
	shared := ""
	if g.Unified {
		shared = ", shared with the CPU"
	}
	switch {
	case g.FreeBytes != nil && g.MemoryBytes > 0:
		return fmt.Sprintf("%s: %s free of %s%s", g.Model, humanBytes(*g.FreeBytes), humanBytes(g.MemoryBytes), shared)
	case g.MemoryBytes > 0:
		return fmt.Sprintf("%s (%s%s)", g.Model, humanBytes(g.MemoryBytes), shared)
	}
	return g.Model + " (memory unknown)"
}

func printInventory(r inventory.Report) {
	fmt.Printf("role         %s\n", r.Role)
	fmt.Printf("hardware     %s/%s, %d x CPU", r.OS, r.Arch, r.CPUCount)
	if r.CPUModel != "" {
		fmt.Printf(" (%s)", r.CPUModel)
	}
	fmt.Println()
	fmt.Printf("memory       %s\n", humanBytes(r.MemoryBytes))
	if r.DiskTotalBytes > 0 {
		fmt.Printf("disk         %s free of %s\n", humanBytes(r.DiskFreeBytes), humanBytes(r.DiskTotalBytes))
	}
	for _, g := range r.GPUs {
		fmt.Printf("gpu          %s\n", gpuText(g))
	}
	if len(r.GPUs) == 0 {
		fmt.Printf("gpu          none detected\n")
	}
	printInbound(r.Inbound)
	if len(r.Runtimes) == 0 {
		fmt.Printf("ai           no runtime detected\n")
	}
	for _, rt := range r.Runtimes {
		models := make([]string, 0, len(rt.Models))
		for _, m := range rt.Models {
			if m.Loaded {
				models = append(models, m.Name+" (loaded)")
			} else {
				models = append(models, m.Name)
			}
		}
		fmt.Printf("ai           %s: %s\n", rt.Kind, strings.Join(models, ", "))
	}
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func endpoints(p home.Peer) string {
	if len(p.Addrs) == 0 {
		return "no reachable address"
	}
	parts := make([]string, 0, len(p.Addrs))
	for _, a := range p.Addrs {
		parts = append(parts, netip.AddrPortFrom(a, uint16(p.Port)).String())
	}
	return strings.Join(parts, ", ")
}

// serveAgentAPI exposes this agent to the LAN: pairing, the dashboard, and the
// AI router.
func serveAgentAPI(id *identity.Identity, store *home.Store, port int) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"machine_id":  id.ID,
			"name":        id.Name,
			"fingerprint": id.Fingerprint(),
		})
	})

	// Proof of possession: a peer sends a nonce, we sign it with our key so
	// the peer can verify we hold the key behind the id we claim.
	mux.HandleFunc("POST /v1/identity/prove", func(w http.ResponseWriter, r *http.Request) {
		var challenge pairing.Challenge
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&challenge); err != nil {
			http.Error(w, "bad challenge", http.StatusBadRequest)
			return
		}
		att, err := pairing.Answer(id, challenge)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(att)
	})

	// The dashboard and the data it draws. Served to the whole LAN by design
	// (open jaggery.local from any device in the house); it exposes inventory,
	// not secrets, and binds only to the local network.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboard.Index())
	})
	mux.HandleFunc("GET /v1/house", func(w http.ResponseWriter, r *http.Request) {
		dir, err := identity.StateDir()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snap, err := house.Load(dir)
		if err != nil {
			// No snapshot yet is not an error for the UI; send an empty house.
			snap = house.Snapshot{UpdatedAt: time.Now().UTC()}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})

	mux.HandleFunc("POST "+pairing.JoinPath, joinHandler(id, store))

	// The house's AI activity, for the dashboard: open to the LAN like the
	// rest of it (decision 2: people in the house are trusted). Metadata only.
	mux.HandleFunc("GET "+houseActivityPath, func(w http.ResponseWriter, r *http.Request) {
		if activityRec == nil {
			http.NotFound(w, r)
			return
		}
		houseActivityHandler(id, store, activityRec.log)(w, r)
	})

	registerRouter(mux, id, store)
	registerMCP(mux, id, store)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("listening on port %d: %w", port, err)
	}

	// The whole LAN port sits behind the guard: home network only, local
	// names only, and no cross-origin changes from web pages.
	guard, err := languard.New(languard.Options{
		PeerAddr: func(a netip.Addr) bool { return approvedAddr(store, a) },
		Logf:     func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
		Forward:  forwardAppName,
	})
	if err != nil {
		listener.Close()
		return nil, err
	}
	server := &http.Server{Handler: guard.Wrap(mux), ReadHeaderTimeout: 5 * time.Second, ErrorLog: quietLog(), IdleTimeout: 2 * time.Minute}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "warning: agent api stopped:", err)
		}
	}()
	return server, nil
}

// approvedAddr reports whether a is an address of an approved machine, which
// may sit beyond the home network (jag join --at).
func approvedAddr(store *home.Store, a netip.Addr) bool {
	peers, err := store.List()
	if err != nil {
		return false
	}
	for _, p := range peers {
		if p.Trust != home.Approved {
			continue
		}
		for _, pa := range p.Addrs {
			if pa.Unmap() == a {
				return true
			}
		}
	}
	return false
}

// serveSecureAPI is the peer-to-peer surface behind mutual TLS: a connection
// is accepted only if the caller presents a certificate whose key the store
// has approved. Everything beyond first-contact pairing will live here.
func serveSecureAPI(id *identity.Identity, store *home.Store, port int) (*http.Server, error) {
	tlsCfg, err := secure.ServerConfig(id, store)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"machine_id": id.ID,
			"name":       id.Name,
			"message":    "you are an approved machine in this home",
		})
	})

	// Inventory is only shared with approved peers, so it lives on the
	// secured listener. Collected per request — cheap, and always current.
	mux.HandleFunc("GET /v1/inventory", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(collectInventory())
	})

	mux.HandleFunc("POST "+reach.Path, reachHandler(id))
	mux.HandleFunc("POST "+filesPath, filesHandler(identity.StateDir, peerNameFrom(store)))
	mux.HandleFunc("GET "+pairing.RosterPath, rosterHandler(id, store))
	mux.HandleFunc("GET "+approvalsPath, approvalsHandler)
	mux.HandleFunc("GET "+activityPath, func(w http.ResponseWriter, r *http.Request) {
		if activityRec == nil {
			http.NotFound(w, r)
			return
		}
		activityHandler(activityRec.log)(w, r)
	})
	if dir, err := identity.StateDir(); err == nil {
		relayHandlers(mux, dir)
	}

	// An approved peer's router chose us to serve a model; run it locally.
	mux.HandleFunc("POST /v1/infer", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "reading request")
			return
		}
		if p := pausedHere(); p != nil {
			writeAPIError(w, http.StatusServiceUnavailable, fmt.Sprintf("this machine has paused AI requests %s", pauseEnd(p)))
			return
		}
		if err := serveInfer(r.Context(), w, body); err != nil {
			writeAPIError(w, http.StatusBadGateway, err.Error())
		}
	})

	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", port), tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("listening on secured port %d: %w", port, err)
	}

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ErrorLog: quietLog(), IdleTimeout: 2 * time.Minute}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "warning: secured api stopped:", err)
		}
	}()
	return server, nil
}

func printInterfaces(usable []netif.Interface, skipped map[string]string) {
	if len(usable) == 0 {
		fmt.Println("announce on  nothing: no usable network interface, so other")
		fmt.Println("             machines cannot reach this one")
	}
	for n, iface := range usable {
		label := "announce on"
		if n > 0 {
			label = ""
		}
		fmt.Printf("%-12s %s  %s\n", label, iface.Name, joinAddrs(iface.Addrs))
	}

	names := make([]string, 0, len(skipped))
	for name := range skipped {
		names = append(names, name)
	}
	sort.Strings(names)
	for n, name := range names {
		label := "ignoring"
		if n > 0 {
			label = ""
		}
		fmt.Printf("%-12s %s  (%s)\n", label, name, skipped[name])
	}
}

func joinAddrs(addrs []netip.Addr) string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return strings.Join(out, ", ")
}
