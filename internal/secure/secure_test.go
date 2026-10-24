package secure

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

func newIdentity(t *testing.T, dir string) *identity.Identity {
	t.Helper()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	id, _, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return id
}

// keySet is a test Approver backed by a fixed set of keys.
type keySet map[string]bool

func approve(keys ...ed25519.PublicKey) keySet {
	s := keySet{}
	for _, k := range keys {
		s[string(k)] = true
	}
	return s
}

func (s keySet) IsApproved(key ed25519.PublicKey) bool { return s[string(key)] }

// serve starts an mTLS server that only approved clients may reach, returning
// its address.
func serve(t *testing.T, server *identity.Identity, approver Approver) string {
	t.Helper()
	cfg, err := ServerConfig(server, approver)
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func get(client *identity.Identity, serverKey ed25519.PublicKey, addr string) error {
	cfg, err := ClientConfig(client, serverKey)
	if err != nil {
		return err
	}
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: cfg},
		Timeout:   3 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func TestApprovedPeerConnects(t *testing.T) {
	server := newIdentity(t, t.TempDir())
	client := newIdentity(t, t.TempDir())

	addr := serve(t, server, approve(client.PublicKey))

	if err := get(client, server.PublicKey, addr); err != nil {
		t.Fatalf("an approved client should connect, got: %v", err)
	}
}

// The whole point: a machine the server has not approved is refused at the TLS
// layer, before any request is served.
func TestUnapprovedClientIsRejected(t *testing.T) {
	server := newIdentity(t, t.TempDir())
	client := newIdentity(t, t.TempDir())
	stranger := newIdentity(t, t.TempDir())

	// Server approves only the stranger, not our client.
	addr := serve(t, server, approve(stranger.PublicKey))

	if err := get(client, server.PublicKey, addr); err == nil {
		t.Fatal("an unapproved client must not connect")
	}
}

// The client pins the server's key: a server presenting a different key than
// expected is refused, which is what defeats a man-in-the-middle.
func TestClientRejectsWrongServerKey(t *testing.T) {
	server := newIdentity(t, t.TempDir())
	client := newIdentity(t, t.TempDir())
	impostor := newIdentity(t, t.TempDir())

	addr := serve(t, server, approve(client.PublicKey))

	// Client expects the impostor's key but reaches the real server.
	if err := get(client, impostor.PublicKey, addr); err == nil {
		t.Fatal("client must reject a server whose key it did not pin")
	}
}

func TestNoClientCertificateIsRejected(t *testing.T) {
	server := newIdentity(t, t.TempDir())
	addr := serve(t, server, approve()) // approves nobody

	// A plain TLS client with no certificate must not get through.
	plain := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   3 * time.Second,
	}
	_, err := plain.Get("https://" + addr + "/")
	if err == nil {
		t.Fatal("a client with no certificate must be rejected")
	}
	_ = net.IPv4zero
}
