// Package appnames forwards requests for the house's app names (track W,
// tech-specs/app-names.md): a request whose Host is chat.local goes to the app
// the owner named chat, on its machine and port.
//
// Forwarding never reaches anything the house couldn't already reach: the
// target is always the app's own machine, at the address the house knows it
// by, on the port its inventory lists. An app that answers only on localhost
// stays unreachable, as it is from every other device.
package appnames

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/apps"
	"github.com/jaggery-dev/jaggery/internal/house"
)

// LoopHeader marks a request Jaggery forwarded. One that comes back carrying
// it (an app name pointed at another agent's port) is refused, not passed on
// for ever.
const LoopHeader = "X-Jaggery-Forwarded"

// Target is where one app name goes.
type Target struct {
	Name    string // chat
	App     string // Open WebUI
	Machine string // desktop
	// MachineID breaks ties when two machines give the same name.
	MachineID string
	Addr      netip.AddrPort
}

// Table maps app names to their targets.
type Table map[string]Target

// FromHouse builds the table from the house as this machine sees it: every
// named app on this machine (at self, its LAN address) and on peers it can
// reach. When two machines give the same name, the one whose id sorts first
// gets it. skipPorts are Jaggery's own ports, never forwarded to.
func FromHouse(snap house.Snapshot, self netip.Addr, skipPorts ...int) Table {
	t := Table{}
	for _, m := range snap.All() {
		var addr netip.Addr
		switch {
		case m.Self:
			addr = self
		case m.Reachable && len(m.Addrs) > 0 && !m.Incompatible:
			addr = m.Addrs[0]
		}
		if !addr.IsValid() {
			continue
		}
		for _, a := range m.Inventory.Apps {
			if a.LocalName == "" || a.URL != "" || a.Port <= 0 || a.Port > 65535 || apps.CheckName(a.LocalName) != nil || skipped(a.Port, skipPorts) {
				continue
			}
			if old, ok := t[a.LocalName]; ok && old.MachineID < m.MachineID {
				continue
			}
			t[a.LocalName] = Target{Name: a.LocalName, App: a.Name, Machine: m.Name, MachineID: m.MachineID,
				Addr: netip.AddrPortFrom(addr.Unmap(), uint16(a.Port))}
		}
	}
	return t
}

func skipped(port int, list []int) bool {
	for _, p := range list {
		if p == port {
			return true
		}
	}
	return false
}

// Names lists the table's names, sorted.
func (t Table) Names() []string {
	out := make([]string, 0, len(t))
	for n := range t {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lookup finds the target for a Host header: "chat.local", "chat.local:80"
// or "chat.local.". Other names, such as jaggery.local, aren't app names.
func (t Table) Lookup(host string) (Target, bool) {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	name, ok := strings.CutSuffix(h, ".local")
	if !ok {
		return Target{}, false
	}
	tg, ok := t[name]
	return tg, ok
}

// Forwarder passes requests for app names on to the apps.
type Forwarder struct {
	// Table returns the current names; it is called on every request.
	Table func() Table
	proxy *httputil.ReverseProxy
}

// NewForwarder builds a Forwarder. The transport ignores proxy settings
// from the environment: the app is on the home network.
func NewForwarder(table func() Table) *Forwarder {
	f := &Forwarder{Table: table}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		// No response timeout: a model loading from disk can take minutes
		// to send its first token.
	}
	f.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			tg := r.In.Context().Value(targetKey{}).(Target)
			r.SetURL(&url.URL{Scheme: "http", Host: tg.Addr.String()})
			// The app sees the name it was opened by, so links it builds
			// from the request point at the name, and the X-Forwarded-*
			// headers say who asked and how.
			r.Out.Host = r.In.Host
			r.SetXForwarded()
			r.Out.Header.Set(LoopHeader, "1")
		},
		Transport: transport,
		// Pass every chunk on at once: streamed answers arrive word by word.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			tg := r.Context().Value(targetKey{}).(Target)
			msg := fmt.Sprintf("%s.local: %s on %s didn't answer (%s). It may be off; open it as http://%s.local:%d to check.",
				tg.Name, tg.App, tg.Machine, short(err), strings.ToLower(tg.Machine), tg.Addr.Port())
			http.Error(w, msg, http.StatusBadGateway)
		},
	}
	return f
}

type targetKey struct{}

// Handler returns the handler for r if its Host is an app name, or nil.
func (f *Forwarder) Handler(r *http.Request) http.Handler {
	if f == nil || f.Table == nil {
		return nil
	}
	tg, ok := f.Table().Lookup(r.Host)
	if !ok {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(LoopHeader) != "" {
			http.Error(w, fmt.Sprintf("%s.local points back at Jaggery; name the app itself, not a Jaggery port", tg.Name), http.StatusLoopDetected)
			return
		}
		f.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), targetKey{}, tg)))
	})
}

func short(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	return err.Error()
}
