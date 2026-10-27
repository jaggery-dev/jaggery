package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
)

const (
	activityPath      = "/v1/activity"       // this machine's log, on the secured listener
	houseActivityPath = "/v1/house/activity" // the house's, for the dashboard
)

// recorder logs requests through the router. Each agent has one; nil (and so
// silent) in the CLI and in tests that don't set one up.
type recorder struct {
	log   *activity.Log
	self  string
	store *home.Store
	mu    sync.Mutex
	// lookup names a device from its own mDNS answer; nil until discovery
	// is up. Guarded by mu.
	lookup func(ctx context.Context, addr netip.Addr, timeout time.Duration) (string, error)
	names  map[netip.Addr]cachedName
}

func newRecorder(log *activity.Log, self string, store *home.Store) *recorder {
	return &recorder{log: log, self: self, store: store, names: map[netip.Addr]cachedName{}}
}

func (r *recorder) setLookup(f func(ctx context.Context, addr netip.Addr, timeout time.Duration) (string, error)) {
	r.mu.Lock()
	r.lookup = f
	r.mu.Unlock()
}

type cachedName struct {
	name string
	at   time.Time
}

var activityRec *recorder

const nameTTL = 10 * time.Minute

// record logs one request. Runs after the reply has gone out: naming a phone
// takes an mDNS round trip, which must not slow anyone's chat.
func (r *recorder) record(req *http.Request, e activity.Entry) {
	if r == nil {
		return
	}
	e.App = activity.AppName(req.UserAgent())
	if req.Header.Get("X-OpenWebUI-Chat-Id") != "" || req.Header.Get("X-OpenWebUI-User-Id") != "" {
		e.App = "Open WebUI"
	}
	e.User = req.Header.Get("X-OpenWebUI-User-Name")
	addr, _ := netip.ParseAddrPort(req.RemoteAddr)
	go func() {
		e.Addr = addr.Addr().Unmap().String()
		e.Device = r.deviceName(addr.Addr().Unmap())
		if err := r.log.Append(e); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not log AI activity:", err)
		}
		for _, t := range toolSeen.observe(e) {
			fmt.Printf("tool alert: %s offered %s a new tool, %s (from %s)\n", orUnknown(t.App), t.Model, t.Tool, t.Device)
		}
	}()
}

// deviceName is this machine for loopback, a peer's name for its addresses,
// otherwise what the device calls itself over mDNS, otherwise its address.
func (r *recorder) deviceName(addr netip.Addr) string {
	if addr.IsLoopback() {
		return r.self
	}
	if peers, err := r.store.List(); err == nil {
		for _, p := range peers {
			for _, a := range p.Addrs {
				if a == addr {
					return p.Name
				}
			}
		}
	}

	r.mu.Lock()
	if c, ok := r.names[addr]; ok && time.Since(c.at) < nameTTL {
		r.mu.Unlock()
		return c.name
	}
	lookup := r.lookup
	r.mu.Unlock()

	name := addr.String()
	if lookup != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if found, err := lookup(ctx, addr, time.Second); err == nil && found != "" {
			name = found
		}
		cancel()
	}
	r.mu.Lock()
	r.names[addr] = cachedName{name, time.Now()}
	r.mu.Unlock()
	return name
}

// usageOf reads token counts from a reply: the usage block of a plain reply,
// or of a stream's last chunk when the client asked for one; otherwise the
// stream's chunk count, as an estimate.
func usageOf(m *speedMeter) (prompt, completion int, estimated bool) {
	type usage struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	var u usage
	if json.Unmarshal(m.head.Bytes(), &u) == nil && u.Usage != nil {
		return u.Usage.PromptTokens, u.Usage.CompletionTokens, false
	}
	lines := bytes.Split(m.tail.Bytes(), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if !bytes.HasPrefix(line, []byte("data: {")) || !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}
		var u usage
		if json.Unmarshal(line[len("data: "):], &u) == nil && u.Usage != nil {
			return u.Usage.PromptTokens, u.Usage.CompletionTokens, false
		}
	}
	if m.events > 0 {
		return 0, m.events, true
	}
	return 0, 0, false
}

// activityHandler serves this machine's log for a day to approved peers.
func activityHandler(log *activity.Log) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		day, err := activity.ParseDay(r.URL.Query().Get("day"), time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		entries, err := log.Day(day)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []activity.Entry{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}
}

// houseActivityHandler gathers the day's activity from this machine and
// every reachable approved peer, newest first.
func houseActivityHandler(id *identity.Identity, store *home.Store, log *activity.Log) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dayParam := r.URL.Query().Get("day")
		day, err := activity.ParseDay(dayParam, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		own, _ := log.Day(day)
		lists := [][]activity.Entry{routedBy(own, id.Name)}

		if peers, err := store.List(); err == nil {
			var (
				mu sync.Mutex
				wg sync.WaitGroup
			)
			for _, p := range peers {
				if p.Trust != home.Approved || !p.Verified() || len(p.Addrs) == 0 {
					continue
				}
				wg.Add(1)
				go func(p home.Peer) {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
					defer cancel()
					var entries []activity.Entry
					err := peerGet(ctx, id, p, activityPath+"?day="+day.Format("2006-01-02"), func(body io.Reader) error {
						return json.NewDecoder(io.LimitReader(body, 16<<20)).Decode(&entries)
					})
					if err != nil {
						return // offline, or older than G2
					}
					mu.Lock()
					lists = append(lists, routedBy(entries, p.Name))
					mu.Unlock()
				}(p)
			}
			wg.Wait()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"day":     day.Format("2006-01-02"),
			"entries": activity.Merge(lists...),
		})
	}
}

func routedBy(entries []activity.Entry, name string) []activity.Entry {
	for i := range entries {
		entries[i].RoutedBy = name
	}
	return entries
}

// localAgent is the agent on this machine; tests point it at a test server.
var localAgent = func() string { return fmt.Sprintf("http://127.0.0.1:%d", discovery.AgentPort) }

// showActivity is `jag activity [day]`: the house's AI activity, from the
// local agent.
func showActivity(args []string) error {
	day := ""
	if len(args) > 0 {
		day = args[0]
	}
	if _, err := activity.ParseDay(day, time.Now()); err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(localAgent() + houseActivityPath + "?day=" + day)
	if err != nil {
		return errNotRunning(err)
	}
	defer resp.Body.Close()
	var out struct {
		Day     string           `json:"day"`
		Entries []activity.Entry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if jsonOutput {
		if out.Entries == nil {
			out.Entries = []activity.Entry{}
		}
		return emitJSON(out)
	}
	if len(out.Entries) == 0 {
		fmt.Printf("no AI requests through Jaggery on %s\n", out.Day)
		return nil
	}
	fmt.Printf("AI activity on %s: %d request(s)\n\n", out.Day, len(out.Entries))
	for _, e := range out.Entries {
		who := e.Device
		if e.User != "" {
			who = e.User + " on " + e.Device
		}
		tokens := ""
		if e.CompletionTokens > 0 {
			approx := ""
			if e.Estimated {
				approx = "~"
			}
			tokens = fmt.Sprintf(" · %s%d tokens", approx, e.CompletionTokens)
		}
		served := e.ServedBy
		if served == "" {
			served = fmt.Sprintf("not served (%d)", e.Status)
		}
		tools := ""
		if len(e.ToolsOffered) > 0 {
			tools = fmt.Sprintf(" · %d tool(s) offered", len(e.ToolsOffered))
		}
		if len(e.ToolCalls) > 0 {
			tools += " · called " + strings.Join(e.ToolCalls, ", ")
		}
		if len(e.Paused) > 0 {
			tools += " · passed over " + strings.Join(e.Paused, ", ") + " (paused)"
		}
		model := e.Model
		if e.API != "" {
			model += " (" + e.API + ")"
		}
		fmt.Printf("%s  %-28s %-16s %s → %s · %.1fs%s%s\n", e.At.Local().Format("15:04"), trimTo(who, 28),
			trimTo(e.App, 16), model, served, float64(e.DurationMS)/1000, tokens, tools)
	}
	return nil
}

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return strings.TrimSpace(s)
}
