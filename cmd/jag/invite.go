package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/netif"
	"github.com/jaggery-dev/jaggery/internal/pairing"
)

// invite issues a join code for a new machine, from one already in the house.
func invite() error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	if _, err := identity.Load(); err != nil {
		return fmt.Errorf("this machine has no identity yet: run `jag up` first")
	}
	inv, err := pairing.CreateInvite(dir, time.Now())
	if err != nil {
		return err
	}

	fmt.Println("on the machine you want to add, run:")
	fmt.Printf("\n    jag join %s\n\n", inv.Code)
	fmt.Printf("the code works once, until %s.\n", inv.Expires.Local().Format("15:04"))
	if !agentRunning() {
		fmt.Println("\nnote: the agent is not running here; start `jag up` so the other machine can reach it.")
	}
	return nil
}

func agentRunning() bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", discovery.AgentPort))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// joinWithCode finds the house's agents over mDNS and joins whichever one
// issued the code. Both sides approve each other; no address is typed.
func joinWithCode(code string, at string) error {
	id, _, err := identity.LoadOrCreate()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Where to send the code: the address given with --at, for a machine
	// mDNS can't reach (over a VPN or a routed link); otherwise every machine
	// that answers on the local network.
	var targets []joinTarget
	if at != "" {
		t, err := resolveJoinTarget(ctx, at)
		if err != nil {
			return err
		}
		targets = []joinTarget{t}
		fmt.Printf("sending the code to %s...\n", at)
	} else {
		usable, _, err := netif.Usable()
		if err != nil {
			return err
		}
		session, err := discovery.Open(usable)
		if err != nil {
			return err
		}
		defer session.Close()
		fmt.Println("looking for the machine that issued the code...")
		peers, err := session.Find(ctx, id.ID, 3*time.Second)
		if err != nil && len(peers) == 0 {
			return err
		}
		for _, peer := range peers {
			for _, addr := range peer.Addrs {
				targets = append(targets, joinTarget{addr: addr, port: peer.Port})
			}
		}
	}

	store, err := openStore()
	if err != nil {
		return err
	}

	var wrongCode bool
	var lastErr error
	for _, t := range targets {
		verified, err := requestJoin(ctx, code, id, t.addr, t.port)
		switch {
		case errors.Is(err, errNoInviteThere):
			continue
		case errors.Is(err, pairing.ErrWrongCode):
			wrongCode = true
			continue
		case err != nil:
			lastErr = err
			continue
		}

		obs := home.Observation{MachineID: verified.MachineID, Name: verified.Name, Addrs: []netip.Addr{t.addr}, Port: t.port}
		if _, err := store.Adopt(obs, verified.PublicKey, home.Approved, time.Now()); err != nil {
			return err
		}
		fmt.Printf("joined the house through %s (%s)\n", verified.Name, verified.MachineID)
		fmt.Printf("fingerprint %s\n", verified.Fingerprint)
		fmt.Println("both machines now trust each other.")
		return nil
	}

	if wrongCode {
		return fmt.Errorf("the code was not accepted; check it, or run `jag invite` again (5 wrong tries burn a code)")
	}
	if at != "" {
		if lastErr != nil {
			return fmt.Errorf("couldn't reach %s: %w", at, lastErr)
		}
		return fmt.Errorf("%s has no pending invite; run `jag invite` there (codes last %d minutes)", at, int(pairing.InviteTTL.Minutes()))
	}
	return fmt.Errorf("found %d address(es), none with a pending invite; run `jag invite` on a machine "+
		"already in the house (codes last %d minutes), or, if it's beyond this network, join through it: jag join <code> --at <address>",
		len(targets), int(pairing.InviteTTL.Minutes()))
}

// joinTarget is an address where an issued code may be waiting.
type joinTarget struct {
	addr netip.Addr
	port int
}

// resolveJoinTarget reads "198.51.100.7", "desktop.vpn:7373" or similar.
func resolveJoinTarget(ctx context.Context, at string) (joinTarget, error) {
	host, port := splitHostPort(at)
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return joinTarget{}, fmt.Errorf("--at %s: bad port", at)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return joinTarget{addr: a.Unmap(), port: p}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return joinTarget{}, fmt.Errorf("--at %s: can't find that host", at)
	}
	return joinTarget{addr: ips[0].Unmap(), port: p}, nil
}

var errNoInviteThere = errors.New("no invite pending on that machine")

func requestJoin(ctx context.Context, code string, id *identity.Identity, addr netip.Addr, port int) (pairing.Verified, error) {
	req, nonce, err := pairing.NewJoinRequest(code, id.ID, id.Name, id.PublicKey, discovery.AgentPort)
	if err != nil {
		return pairing.Verified{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return pairing.Verified{}, err
	}

	url := "http://" + net.JoinHostPort(addr.String(), strconv.Itoa(port)) + pairing.JoinPath
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return pairing.Verified{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return pairing.Verified{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return pairing.Verified{}, errNoInviteThere
	case http.StatusForbidden:
		// The house machine's guard (internal/languard) refused us before
		// the code was looked at: say why, not "wrong code".
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if why, ok := strings.CutPrefix(strings.TrimSpace(string(msg)), "refused by Jaggery: "); ok {
			return pairing.Verified{}, fmt.Errorf("the house machine refused the connection: %s", why)
		}
		return pairing.Verified{}, pairing.ErrWrongCode
	default:
		return pairing.Verified{}, fmt.Errorf("%s answered %s", url, resp.Status)
	}

	var answer pairing.JoinResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&answer); err != nil {
		return pairing.Verified{}, err
	}
	return pairing.CheckJoinResponse(code, req, nonce, answer)
}

// joinHandler is the inviter's end, on the LAN port: a machine that knows the
// pending code is approved here and gets our proof in return. One join at a
// time, so failure counting and consuming the invite can't race.
func joinHandler(id *identity.Identity, store *home.Store) http.HandlerFunc {
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		dir, err := identity.StateDir()
		if err != nil {
			http.Error(w, "no state dir", http.StatusInternalServerError)
			return
		}
		inv, err := pairing.LoadInvite(dir, time.Now())
		if err != nil {
			http.Error(w, "no invite pending", http.StatusNotFound)
			return
		}

		var req pairing.JoinRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		pub, err := pairing.CheckJoinRequest(inv.Code, req)
		if err != nil {
			_ = pairing.RecordFailure(dir, inv)
			http.Error(w, "code not accepted", http.StatusForbidden)
			return
		}

		att, err := pairing.Answer(id, pairing.Challenge{Nonce: req.Nonce})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		obs := home.Observation{MachineID: req.MachineID, Name: req.Name, Port: req.Port}
		if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
			obs.Addrs = []netip.Addr{ap.Addr().Unmap()}
		}
		if _, err := store.Adopt(obs, pub, home.Approved, time.Now()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = pairing.DeleteInvite(dir)
		fmt.Printf("peer joined with a code: %s (%s)\n", req.Name, req.MachineID)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pairing.Accept(inv.Code, att, req))
	}
}
