package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/pairing"
)

// rosterHandler serves this machine's signed list of approved peers. It lives
// on the secured listener, so only approved machines can read it.
func rosterHandler(id *identity.Identity, store *home.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peers, err := store.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		signed, err := pairing.SignRoster(id, rosterOf(id, peers, time.Now()))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(signed)
	}
}

// rosterOf lists the peers this machine vouches for: approved, with a proven key.
func rosterOf(id *identity.Identity, peers []home.Peer, now time.Time) pairing.Roster {
	roster := pairing.Roster{Issuer: id.ID, IssuedAt: now.UTC(), Members: []pairing.Member{}}
	for _, p := range peers {
		if p.Trust != home.Approved || !p.Verified() {
			continue
		}
		roster.Members = append(roster.Members, pairing.Member{
			MachineID: p.MachineID,
			Name:      p.Name,
			PublicKey: p.PublicKey,
			Addrs:     p.Addrs,
			Port:      p.Port,
		})
	}
	return roster
}

// syncMembers reads every reachable approved peer's roster and approves the
// machines they vouch for, so joining through one machine joins the house.
// Trust spreads one hop per scan; peers that are offline, or run an agent
// that predates rosters, are skipped quietly.
func syncMembers(ctx context.Context, id *identity.Identity, store *home.Store) {
	peers, err := store.List()
	if err != nil {
		return
	}
	for _, p := range peers {
		if p.Trust != home.Approved || !p.Verified() || len(p.Addrs) == 0 {
			continue
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		roster, err := fetchRoster(fetchCtx, id, p)
		cancel()
		if err != nil {
			continue
		}
		adoptRoster(id, store, p, roster)
	}
}

// adoptRoster approves the members of a roster that voucher sent.
func adoptRoster(id *identity.Identity, store *home.Store, voucher home.Peer, roster pairing.Roster) {
	for _, m := range roster.Members {
		if m.MachineID == id.ID {
			continue
		}
		obs := home.Observation{MachineID: m.MachineID, Name: m.Name, Addrs: m.Addrs, Port: m.Port}
		if _, err := store.Vouch(obs, m.PublicKey, voucher.MachineID, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not record vouched peer:", err)
		}
	}
}

func fetchRoster(ctx context.Context, id *identity.Identity, peer home.Peer) (pairing.Roster, error) {
	key := ed25519.PublicKey(peer.PublicKey)
	client, err := peerHTTPClient(id, key, 5*time.Second)
	if err != nil {
		return pairing.Roster{}, err
	}

	addr := net.JoinHostPort(peer.Addrs[0].String(), strconv.Itoa(discovery.SecurePort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+pairing.RosterPath, nil)
	if err != nil {
		return pairing.Roster{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return pairing.Roster{}, err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return pairing.Roster{}, fmt.Errorf("%s answered %s", peer.Name, resp.Status)
	}

	var signed pairing.SignedRoster
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&signed); err != nil {
		return pairing.Roster{}, fmt.Errorf("decoding roster: %w", err)
	}
	return pairing.OpenRoster(signed, key)
}
