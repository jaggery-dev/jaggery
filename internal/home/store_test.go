package home

import (
	"crypto/ed25519"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// ed25519Key pads arbitrary test bytes to a valid key length so IsApproved's
// key comparison runs against the right-sized value.
func ed25519Key(seed []byte) ed25519.PublicKey {
	key := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(key, seed)
	return key
}

func obs(id, name, addr string) Observation {
	return Observation{
		MachineID: id,
		Name:      name,
		Addrs:     []netip.Addr{netip.MustParseAddr(addr)},
		Port:      7373,
	}
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestNewPeerStartsPending(t *testing.T) {
	s := openTemp(t)
	if err := s.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now()); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	peers, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(peers) != 1 || peers[0].Trust != Pending {
		t.Fatalf("want one pending peer, got %+v", peers)
	}
}

// Re-observing an approved peer must not demote it back to pending — otherwise
// every discovery scan would silently revoke trust.
func TestObservePreservesTrust(t *testing.T) {
	s := openTemp(t)
	start := time.Now()
	if err := s.Observe(obs("abc123", "nas", "192.0.2.70"), start); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTrust("abc", Approved); err != nil {
		t.Fatalf("SetTrust: %v", err)
	}

	// A later scan sees the peer again with a new address.
	later := start.Add(time.Minute)
	if err := s.Observe(Observation{MachineID: "abc123", Name: "nas", Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.99")}, Port: 7373}, later); err != nil {
		t.Fatal(err)
	}

	peers, _ := s.List()
	if peers[0].Trust != Approved {
		t.Errorf("trust = %q, want it to stay approved across re-observation", peers[0].Trust)
	}
	if !peers[0].FirstSeen.Equal(start) {
		t.Errorf("first-seen moved to %v, want it pinned to %v", peers[0].FirstSeen, start)
	}
	if peers[0].Addrs[0].String() != "192.0.2.99" {
		t.Errorf("address not refreshed: %v", peers[0].Addrs)
	}
}

func TestSetTrustByPrefix(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc123def", "nas", "192.0.2.70"), time.Now())

	peer, err := s.SetTrust("abc1", Approved)
	if err != nil {
		t.Fatalf("SetTrust: %v", err)
	}
	if peer.Name != "nas" {
		t.Errorf("resolved the wrong peer: %q", peer.Name)
	}
}

func TestAmbiguousAndMissingPrefixes(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc111", "nas", "192.0.2.70"), time.Now())
	s.Observe(obs("abc222", "pi", "192.0.2.73"), time.Now())

	if _, err := s.SetTrust("abc", Approved); err != ErrAmbiguous {
		t.Errorf("want ErrAmbiguous for a shared prefix, got %v", err)
	}
	if _, err := s.SetTrust("zzz", Approved); err != ErrNotFound {
		t.Errorf("want ErrNotFound for an unknown prefix, got %v", err)
	}
	if _, err := s.SetTrust("", Approved); err != ErrNotFound {
		t.Errorf("want ErrNotFound for an empty prefix, got %v", err)
	}
}

func TestAttachKeyPinsWithoutChangingTrust(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now())

	if err := s.AttachKey("abc123", []byte("a-public-key")); err != nil {
		t.Fatalf("AttachKey: %v", err)
	}

	peers, _ := s.List()
	if !peers[0].Verified() {
		t.Error("peer should be verified after AttachKey")
	}
	if peers[0].Trust != Pending {
		t.Errorf("trust changed to %q, AttachKey must not touch it", peers[0].Trust)
	}
}

func TestAttachKeyIsANoOpForUnknownOrKeyedPeers(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now())
	s.AttachKey("abc123", []byte("first-key"))

	// Unknown peer: silently ignored.
	if err := s.AttachKey("zzz999", []byte("k")); err != nil {
		t.Fatalf("AttachKey for unknown peer: %v", err)
	}
	// Already keyed: must not be overwritten.
	s.AttachKey("abc123", []byte("second-key"))

	peers, _ := s.List()
	if string(peers[0].PublicKey) != "first-key" {
		t.Errorf("key was overwritten: %q", peers[0].PublicKey)
	}
}

func TestIsApprovedChecksKeyNotID(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now())
	realKey := ed25519Key([]byte("the-real-key"))
	s.AttachKey("abc123", realKey)
	s.SetTrust("abc123", Approved)

	if !s.IsApproved(realKey) {
		t.Error("the approved peer's key should be accepted")
	}
	// An impostor claiming the same id with a different key must be refused.
	if s.IsApproved(ed25519Key([]byte("a-different-key"))) {
		t.Error("a different key must not be accepted even for an approved id")
	}
}

func TestIsApprovedRejectsPendingAndKeyless(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now())
	key := ed25519Key([]byte("k"))
	s.AttachKey("abc123", key)
	// Still pending — not approved — so the key must not be accepted.
	if s.IsApproved(key) {
		t.Error("a pending peer's key must not be approved")
	}
}

func TestForget(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now())
	if _, err := s.Forget("abc"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	peers, _ := s.List()
	if len(peers) != 0 {
		t.Errorf("peer survived Forget: %+v", peers)
	}
}

func TestListOrdersApprovedFirst(t *testing.T) {
	s := openTemp(t)
	s.Observe(obs("a1", "zebra", "192.0.2.70"), time.Now())
	s.Observe(obs("a2", "apple", "192.0.2.71"), time.Now())
	s.Observe(obs("a3", "mango", "192.0.2.72"), time.Now())
	s.SetTrust("a1", Approved) // zebra, approved

	peers, _ := s.List()
	if peers[0].Name != "zebra" {
		t.Errorf("approved peer should sort first, got %q", peers[0].Name)
	}
	if peers[1].Name != "apple" || peers[2].Name != "mango" {
		t.Errorf("pending peers should sort by name, got %q then %q", peers[1].Name, peers[2].Name)
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, _ := Open(dir)
	s1.Observe(obs("abc123", "nas", "192.0.2.70"), time.Now())
	s1.SetTrust("abc", Approved)

	s2, _ := Open(dir)
	peers, err := s2.List()
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(peers) != 1 || peers[0].Trust != Approved {
		t.Errorf("state did not survive reopen: %+v", peers)
	}
}

// The agent and the CLI mutate the same store from different goroutines (and
// processes); the lock must serialise them without losing writes.
func TestConcurrentMutationsDoNotLose(t *testing.T) {
	s := openTemp(t)
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a'+i)) + "0000"
			if err := s.Observe(obs(id, "m", "192.0.2.70"), time.Now()); err != nil {
				t.Errorf("Observe: %v", err)
			}
		}(i)
	}
	wg.Wait()

	peers, _ := s.List()
	if len(peers) != n {
		t.Errorf("got %d peers after %d concurrent writes, want %d", len(peers), n, n)
	}
}

func TestVouchApprovesAnUnknownMachine(t *testing.T) {
	s := openTemp(t)
	changed, err := s.Vouch(obs("fourth1", "fourth", "192.0.2.80"), ed25519Key([]byte("fourth")), "pi1", time.Now())
	if err != nil || !changed {
		t.Fatalf("Vouch: changed %v, %v", changed, err)
	}
	p, err := s.Get("fourth1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Trust != Approved || !p.Verified() || p.ApprovedVia != "pi1" {
		t.Fatalf("vouched peer stored as %+v", p)
	}
	if !s.IsApproved(ed25519Key([]byte("fourth"))) {
		t.Fatal("vouched peer's key is not let in over mTLS")
	}
}

// A pending machine discovered here keeps the address discovery found, which
// is fresher than the voucher's.
func TestVouchApprovesAPendingMachineAndKeepsItsAddress(t *testing.T) {
	s := openTemp(t)
	if err := s.Observe(obs("fourth1", "fourth", "192.0.2.80"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Vouch(obs("fourth1", "fourth", "10.0.0.9"), ed25519Key([]byte("fourth")), "pi1", time.Now()); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("fourth1")
	if p.Trust != Approved || p.Addrs[0] != netip.MustParseAddr("192.0.2.80") {
		t.Fatalf("got %+v", p)
	}
}

// A machine a person approved here stays approved here, not "via" another.
func TestVouchLeavesDirectApprovalAlone(t *testing.T) {
	s := openTemp(t)
	key := ed25519Key([]byte("laptop"))
	if _, err := s.Adopt(obs("laptop1", "laptop", "192.0.2.55"), key, Approved, time.Now()); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Vouch(obs("laptop1", "laptop", "192.0.2.55"), key, "pi1", time.Now())
	if err != nil || changed {
		t.Fatalf("Vouch on a direct approval: changed %v, %v", changed, err)
	}
	if p, _ := s.Get("laptop1"); p.ApprovedVia != "" {
		t.Fatalf("direct approval became vouched: %+v", p)
	}
}

// Forgetting a machine must stick: the next roster that lists it can't undo
// what a person did. Approving it directly again lifts that.
func TestForgottenMachineIsNotVouchedBackIn(t *testing.T) {
	s := openTemp(t)
	key := ed25519Key([]byte("old"))
	if _, err := s.Vouch(obs("old1", "old", "192.0.2.90"), key, "pi1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget("old1"); err != nil {
		t.Fatal(err)
	}
	if changed, _ := s.Vouch(obs("old1", "old", "192.0.2.90"), key, "pi1", time.Now()); changed {
		t.Fatal("a roster brought a forgotten machine back")
	}
	if s.IsApproved(key) {
		t.Fatal("forgotten machine is still let in")
	}

	if _, err := s.Adopt(obs("old1", "old", "192.0.2.90"), key, Approved, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget("old1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adopt(obs("old1", "old", "192.0.2.90"), key, Approved, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !s.IsApproved(key) {
		t.Fatal("re-approving a forgotten machine directly did not stick")
	}
}

func TestPreferAddrPutsTheWorkingAddressFirst(t *testing.T) {
	s := openTemp(t)
	o := obs("desktop1", "desktop", "192.0.2.51")
	o.Addrs = append(o.Addrs, netip.MustParseAddr("192.0.2.65"))
	if err := s.Observe(o, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.PreferAddr("desktop1", netip.MustParseAddr("192.0.2.65")); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("desktop1")
	if len(p.Addrs) != 2 || p.Addrs[0] != netip.MustParseAddr("192.0.2.65") {
		t.Fatalf("addrs %v", p.Addrs)
	}
}
