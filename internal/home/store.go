// Package home persists this machine's view of the other machines in the
// house: who they are and whether they have been approved.
//
// Two processes touch this store at once — the running agent upserts peers as
// it discovers them, while `jag approve` mutates trust from the command line.
// Writes therefore take a cross-process lock on a sibling .lock file (not the
// data file, which is replaced by rename), and the data file is swapped in
// atomically so a concurrent reader always sees a whole file.
package home

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	fileName = "peers.json"
	lockName = "peers.lock"
)

// Trust is how much this machine trusts a peer.
type Trust string

const (
	// Pending: discovered on the network but not yet approved by a human.
	Pending Trust = "pending"
	// Approved: a person confirmed this machine belongs in the house.
	Approved Trust = "approved"
)

type Peer struct {
	MachineID string       `json:"machine_id"`
	Name      string       `json:"name"`
	Addrs     []netip.Addr `json:"addrs"`
	Port      int          `json:"port"`
	Trust     Trust        `json:"trust"`
	// PublicKey is set once the peer has proven it holds the key behind its
	// id (see internal/pairing). Empty means discovered but not yet verified.
	PublicKey []byte `json:"public_key,omitempty"`
	// ApprovedVia is the machine id of the approved peer that vouched for
	// this one (see Vouch). Empty when a person approved it here.
	ApprovedVia string    `json:"approved_via,omitempty"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// Verified reports whether this peer's key has been proven.
func (p Peer) Verified() bool { return len(p.PublicKey) > 0 }

// Observation is what discovery hands us about a peer right now.
type Observation struct {
	MachineID string
	Name      string
	Addrs     []netip.Addr
	Port      int
}

type data struct {
	Peers map[string]*Peer `json:"peers"`
	// Forgotten holds the machines a person removed here, so that another
	// machine's roster doesn't quietly bring them back. Approving one directly
	// again lifts it.
	Forgotten map[string]time.Time `json:"forgotten,omitempty"`
}

// Store is a handle to the on-disk peer list. It is cheap to create; each
// operation reads and writes the file under a lock, so a Store never holds
// stale state and multiple processes can share it.
type Store struct {
	dir string
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Observe records a freshly discovered peer. A peer seen for the first time is
// stored as Pending; an existing peer keeps its trust and first-seen time.
func (s *Store) Observe(obs Observation, now time.Time) error {
	return s.mutate(func(d *data) error {
		peer, ok := d.Peers[obs.MachineID]
		if !ok {
			peer = &Peer{MachineID: obs.MachineID, Trust: Pending, FirstSeen: now}
			d.Peers[obs.MachineID] = peer
		}
		peer.Name = obs.Name
		peer.Addrs = obs.Addrs
		peer.Port = obs.Port
		peer.LastSeen = now
		return nil
	})
}

// Adopt records a peer whose key has been verified, setting its trust. Used by
// `jag join`, where the person deliberately reached out to a known address, so
// the peer is trusted and pinned to its key in one atomic step. An existing
// peer keeps its first-seen time.
func (s *Store) Adopt(obs Observation, publicKey []byte, trust Trust, now time.Time) (*Peer, error) {
	var result *Peer
	err := s.mutate(func(d *data) error {
		peer, ok := d.Peers[obs.MachineID]
		if !ok {
			peer = &Peer{MachineID: obs.MachineID, FirstSeen: now}
			d.Peers[obs.MachineID] = peer
		}
		peer.Name = obs.Name
		if len(obs.Addrs) > 0 {
			peer.Addrs = obs.Addrs
		}
		if obs.Port != 0 {
			peer.Port = obs.Port
		}
		peer.PublicKey = publicKey
		peer.Trust = trust
		peer.LastSeen = now
		if trust == Approved {
			approvedHere(d, peer)
		}
		result = peer
		return nil
	})
	return result, err
}

// Vouch approves a machine because an approved peer, via, lists it in its
// roster. The caller has checked the roster's signature and that the key
// derives the id. Returns true if this changed anything: a machine already
// approved, or one a person forgot here, is left alone. Addresses are only
// filled in when none are known, since discovery's are fresher.
func (s *Store) Vouch(obs Observation, publicKey []byte, via string, now time.Time) (bool, error) {
	changed := false
	err := s.mutate(func(d *data) error {
		if _, gone := d.Forgotten[obs.MachineID]; gone {
			return nil
		}
		peer, ok := d.Peers[obs.MachineID]
		if ok && peer.Trust == Approved {
			return nil
		}
		if !ok {
			peer = &Peer{MachineID: obs.MachineID, Name: obs.Name, FirstSeen: now}
			d.Peers[obs.MachineID] = peer
		}
		if len(peer.Addrs) == 0 {
			peer.Addrs = obs.Addrs
		}
		if peer.Port == 0 {
			peer.Port = obs.Port
		}
		peer.PublicKey = publicKey
		peer.Trust = Approved
		peer.ApprovedVia = via
		changed = true
		return nil
	})
	return changed, err
}

// approvedHere marks a peer as approved by a person on this machine.
func approvedHere(d *data, peer *Peer) {
	peer.ApprovedVia = ""
	delete(d.Forgotten, peer.MachineID)
}

// PreferAddr moves addr to the front of a peer's addresses: the one that
// answered, so calls that use the first address reach it.
func (s *Store) PreferAddr(machineID string, addr netip.Addr) error {
	return s.mutate(func(d *data) error {
		peer, ok := d.Peers[machineID]
		if !ok {
			return nil
		}
		out := []netip.Addr{addr}
		for _, a := range peer.Addrs {
			if a != addr {
				out = append(out, a)
			}
		}
		peer.Addrs = out
		return nil
	})
}

// AttachKey pins a peer's verified public key without changing its trust.
// Used by the agent to verify discovered peers in the background, so that by
// the time a human runs `jag approve` the key is already known. A no-op if the
// peer is unknown or already keyed — an id derives from exactly one key, so a
// key never legitimately changes.
func (s *Store) AttachKey(machineID string, publicKey []byte) error {
	return s.mutate(func(d *data) error {
		peer, ok := d.Peers[machineID]
		if !ok || peer.Verified() {
			return nil
		}
		peer.PublicKey = publicKey
		return nil
	})
}

var (
	ErrNotFound  = errors.New("no peer matches that id")
	ErrAmbiguous = errors.New("that id matches more than one peer")
)

// SetTrust resolves a machine-id prefix to exactly one peer and sets its trust.
func (s *Store) SetTrust(idPrefix string, trust Trust) (*Peer, error) {
	var result *Peer
	err := s.mutate(func(d *data) error {
		peer, err := resolve(d, idPrefix)
		if err != nil {
			return err
		}
		peer.Trust = trust
		if trust == Approved {
			approvedHere(d, peer)
		}
		result = peer
		return nil
	})
	return result, err
}

// Forget removes a peer entirely, and remembers that it was removed so that
// other machines' rosters don't approve it again.
func (s *Store) Forget(idPrefix string) (*Peer, error) {
	var result *Peer
	err := s.mutate(func(d *data) error {
		peer, err := resolve(d, idPrefix)
		if err != nil {
			return err
		}
		delete(d.Peers, peer.MachineID)
		if d.Forgotten == nil {
			d.Forgotten = map[string]time.Time{}
		}
		d.Forgotten[peer.MachineID] = time.Now().UTC()
		result = peer
		return nil
	})
	return result, err
}

// IsApproved reports whether a peer presenting this public key is an approved
// machine. It compares the key, not the id, so an impostor claiming an approved
// id with a different key is not let in. Satisfies secure.Approver.
func (s *Store) IsApproved(key ed25519.PublicKey) bool {
	d, err := s.read()
	if err != nil {
		return false
	}
	for _, p := range d.Peers {
		if p.Trust == Approved && len(p.PublicKey) > 0 && key.Equal(ed25519.PublicKey(p.PublicKey)) {
			return true
		}
	}
	return false
}

// Get resolves a machine-id prefix to exactly one peer.
func (s *Store) Get(idPrefix string) (Peer, error) {
	d, err := s.read()
	if err != nil {
		return Peer{}, err
	}
	peer, err := resolve(d, idPrefix)
	if err != nil {
		return Peer{}, err
	}
	return *peer, nil
}

// List returns all known peers, approved first, then by name.
func (s *Store) List() ([]Peer, error) {
	d, err := s.read()
	if err != nil {
		return nil, err
	}
	peers := make([]Peer, 0, len(d.Peers))
	for _, p := range d.Peers {
		peers = append(peers, *p)
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Trust != peers[j].Trust {
			return peers[i].Trust == Approved
		}
		return peers[i].Name < peers[j].Name
	})
	return peers, nil
}

func resolve(d *data, idPrefix string) (*Peer, error) {
	if idPrefix == "" {
		return nil, ErrNotFound
	}
	var match *Peer
	for id, peer := range d.Peers {
		if strings.HasPrefix(id, idPrefix) {
			if match != nil {
				return nil, ErrAmbiguous
			}
			match = peer
		}
	}
	if match == nil {
		return nil, ErrNotFound
	}
	return match, nil
}

// mutate runs fn against the current data under an exclusive cross-process
// lock, then writes the result back atomically.
func (s *Store) mutate(fn func(*data) error) error {
	lock, err := os.OpenFile(filepath.Join(s.dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock: %w", err)
	}
	defer lock.Close()

	if err := lockFile(lock); err != nil {
		return fmt.Errorf("locking store: %w", err)
	}
	defer unlockFile(lock)

	d, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(d); err != nil {
		return err
	}
	return s.write(d)
}

func (s *Store) read() (*data, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return &data{Peers: map[string]*Peer{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading peer store: %w", err)
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parsing peer store: %w", err)
	}
	if d.Peers == nil {
		d.Peers = map[string]*Peer{}
	}
	return &d, nil
}

func (s *Store) write(d *data) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding peer store: %w", err)
	}
	path := filepath.Join(s.dir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("writing peer store: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saving peer store: %w", err)
	}
	return nil
}
