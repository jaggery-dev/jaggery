package main

import (
	"crypto/ed25519"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/secure"
)

type approveAll struct{}

func (approveAll) IsApproved(ed25519.PublicKey) bool { return true }

// The leak that wedged the laptop: every request to a peer opened a connection
// that was never reused or closed. A hundred requests must share a handful.
func TestPeerRequestsReuseConnections(t *testing.T) {
	server, client := newIdentity(t, "server"), newIdentity(t, "client")
	tlsCfg, err := secure.ServerConfig(server, approveAll{})
	if err != nil {
		t.Fatal(err)
	}
	var opened atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = tlsCfg
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()

	for i := 0; i < 100; i++ {
		c, err := peerHTTPClient(client, server.PublicKey, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		drainClose(resp) // as the agent's callers do
	}
	if n := opened.Load(); n > 2 {
		t.Fatalf("100 requests opened %d connections; want them reused", n)
	}
}
