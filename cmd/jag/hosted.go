package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/hosted"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/router"
)

// The house's exit to hosted models: set up by
// startHosted in the agent; with no state directory (the CLI, most tests)
// it has no providers.
type exitState struct {
	stateDir string
	client   *http.Client
	spend    *hosted.Ledger
	log      *hosted.Log

	mu      sync.Mutex
	config  hosted.Config
	modTime time.Time
	size    int64
	checked time.Time
}

var exit = &exitState{}

// reloadEvery is how often the agent looks for a changed providers.json:
// editing a cap or adding a provider needs no restart.
var reloadEvery = 2 * time.Second

// startHosted opens the spend ledger and the outbound log and loads
// providers.json. If either can't be opened, hosted models stay off: better
// none than unchecked ones.
func startHosted(stateDir string) {
	exit.stateDir = stateDir
	exit.client = &http.Client{Timeout: 10 * time.Minute}
	spend, err := hosted.OpenLedger(stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: hosted models are off: spend ledger:", err)
		return
	}
	log, err := hosted.OpenLog(stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: hosted models are off: outbound log:", err)
		return
	}
	log.Prune(time.Now())
	exit.spend, exit.log = spend, log
	hostedConfig()
}

// hostedConfig is the current providers, reloaded when providers.json
// changes. A file that can't be read turns every provider off, loudly.
func hostedConfig() hosted.Config {
	e := exit
	if e.spend == nil || e.log == nil {
		return hosted.Config{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if !e.checked.IsZero() && now.Sub(e.checked) < reloadEvery {
		return e.config
	}
	first := e.checked.IsZero()
	e.checked = now
	var mod time.Time
	size := int64(-1)
	if info, err := os.Stat(filepath.Join(e.stateDir, hosted.ConfigFile)); err == nil {
		mod, size = info.ModTime(), info.Size()
	}
	if !first && mod.Equal(e.modTime) && size == e.size {
		return e.config
	}
	e.modTime, e.size = mod, size
	cfg, err := hosted.Load(e.stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: hosted models are off:", err)
		cfg = hosted.Config{}
	}
	if len(cfg.Providers) == 0 && len(e.config.Providers) > 0 {
		fmt.Println("hosted: no providers now")
	}
	for _, p := range cfg.Providers {
		fmt.Printf("hosted: %s (%s), $%.4f of $%.2f spent\n",
			p.Name, strings.Join(p.Models, ", "), e.spend.Used(p.Name).USD, p.CapUSD)
	}
	e.config = cfg
	return cfg
}

// hostedModels lists this machine's hosted models for /v1/models.
func hostedModels() []router.MergedModel {
	var out []router.MergedModel
	cfg := hostedConfig()
	for _, p := range cfg.Providers {
		for _, m := range p.Models {
			out = append(out, router.MergedModel{ID: p.Name + "/" + m, Object: "model", OwnedBy: p.Name})
		}
	}
	names := make([]string, 0, len(cfg.Groups))
	for g := range cfg.Groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		out = append(out, router.MergedModel{ID: g, Object: "model", OwnedBy: "jaggery group: " + strings.Join(cfg.Groups[g], ", ")})
	}
	return out
}

// serveHosted sends a chat request to one named provider model, if the model
// is allowed and the cap has room. Every request is logged in the outbound
// log, sent or not, and in the activity log.
func serveHosted(w http.ResponseWriter, r *http.Request, p *hosted.Provider, model string, body []byte, entry activity.Entry) {
	attemptHosted(w, r, p, model, "", body, entry, false)
}

// retryStatuses are provider answers that say "not now" rather than "no":
// a group moves on to its next model when it gets one before any reply.
var retryStatuses = map[int]time.Duration{
	http.StatusTooManyRequests:     time.Minute,
	http.StatusPaymentRequired:     time.Hour,
	http.StatusInternalServerError: 30 * time.Second,
	http.StatusBadGateway:          30 * time.Second,
	http.StatusServiceUnavailable:  30 * time.Second,
	http.StatusGatewayTimeout:      30 * time.Second,
	529:                            time.Minute, // overloaded
}

// attemptHosted sends one request to one provider model. With canRetry, a "not now" answer (or a failed connection)
// before any reply writes nothing to w and returns false, so a group can try
// its next model; the attempt is still logged, as it still left the house.
func attemptHosted(w http.ResponseWriter, r *http.Request, p *hosted.Provider, model, group string, body []byte, entry activity.Entry, canRetry bool) (done bool) {
	start := time.Now()
	out := hosted.Entry{ID: hosted.NewID(start), At: start.UTC(), Provider: p.Name, Model: model, Group: group}
	entry.ServedBy = p.Name + " (hosted)"
	finish := func(status int, reason string) {
		out.Status, out.Reason = status, reason
		out.DurationMS = time.Since(start).Milliseconds()
		out.App = activity.AppName(r.UserAgent())
		if err := exit.log.Append(out); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not write the outbound log:", err)
		}
		if done {
			entry.Status, entry.DurationMS = status, out.DurationMS
			activityRec.record(r, entry)
		}
	}
	fail := func(status int, msg, reason string) bool {
		done = !canRetry
		if done {
			writeAPIError(w, status, msg)
		}
		finish(status, reason)
		return done
	}

	prepared, maxOut, err := hosted.Prepare(body, p, model)
	if err != nil {
		done = true
		writeAPIError(w, http.StatusBadRequest, "reading request: "+err.Error())
		finish(http.StatusBadRequest, "bad request")
		return true
	}
	worstTokens := hosted.EstimatePromptTokens(prepared) + maxOut
	worst := p.Cost(hosted.EstimatePromptTokens(prepared), maxOut)
	if exit.spend.FreeLeft(p, start) >= int64(worstTokens) {
		worst = 0 // inside the free allowance: nothing to hold against the cap
	}
	if !exit.spend.Reserve(p, worst) {
		used := exit.spend.Used(p.Name).USD
		return fail(http.StatusPaymentRequired, fmt.Sprintf("not sent to %s: it could take spending past the cap ($%.4f of $%.2f used; this request could cost up to $%.4f)", p.Name, used, p.CapUSD, worst), "spend cap")
	}
	key, err := p.Key(exit.stateDir)
	if err != nil {
		_ = exit.spend.Settle(p.Name, worst, 0, 0, 0, false)
		return fail(http.StatusServiceUnavailable, err.Error(), "no key")
	}

	// From here the body leaves the house: any attempt counts as sent, as a
	// connection that failed part-way may already have carried it.
	out.Sent, out.BytesSent = true, len(prepared)
	resp, err := openHosted(r.Context(), p, key, prepared)
	if err != nil {
		_ = exit.spend.Settle(p.Name, worst, 0, 0, 0, false)
		coolProvider(p.Name, 30*time.Second)
		return fail(http.StatusBadGateway, err.Error(), err.Error())
	}
	defer resp.Body.Close()
	if cool, retry := retryStatuses[resp.StatusCode]; retry && canRetry {
		head, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = exit.spend.Settle(p.Name, worst, 0, 0, 0, false)
		if s := resp.Header.Get("Retry-After"); s != "" {
			if secs, err := strconv.Atoi(s); err == nil && secs > 0 {
				cool = time.Duration(secs) * time.Second
			}
		}
		coolProvider(p.Name, cool)
		_ = exit.log.SavePayload(out.ID, prepared, head)
		return fail(resp.StatusCode, "", fmt.Sprintf("%d from %s; tried the next model", resp.StatusCode, p.Name))
	}

	done = true
	meter := newSpeedMeter(w)
	capture := &replyCapture{speedMeter: meter}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	capture.WriteHeader(resp.StatusCode)
	if err := copyFlushing(capture, resp.Body); err != nil {
		fmt.Fprintln(os.Stderr, "warning: hosted stream cut short:", err)
	}

	prompt, completion, estimated := usageOf(meter)
	if meter.status == http.StatusOK {
		// What the provider didn't report is charged at the worst case, so
		// a missing usage block can't let spending creep past the cap.
		if prompt == 0 {
			prompt, estimated = hosted.EstimatePromptTokens(prepared), true
		}
		if completion == 0 && estimated {
			completion = maxOut
		}
	}
	cost := exit.spend.Bill(p, prompt, completion, start)
	if err := exit.spend.SettleAt(p.Name, worst, prompt, completion, cost, estimated, p.Free, start); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not save hosted spend:", err)
	}
	out.PromptTokens, out.CompletionTokens, out.Estimated, out.CostUSD = prompt, completion, estimated, cost
	entry.PromptTokens, entry.CompletionTokens, entry.Estimated = prompt, completion, estimated
	entry.ToolCalls = toolCallsOf(meter)
	if err := exit.log.SavePayload(out.ID, prepared, capture.buf.Bytes()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not keep the outbound payload:", err)
	}
	finish(meter.status, "")
	return true
}

// openHosted posts the prepared body to the provider and returns its
// response, unread.
func openHosted(ctx context.Context, p *hosted.Provider, key string, body []byte) (*http.Response, error) {
	url := strings.TrimSuffix(p.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := exit.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.Name, err)
	}
	return resp, nil
}

// serveGroup sends a request to a model group (X5): members are tried in
// order of preference until one answers.
func serveGroup(w http.ResponseWriter, r *http.Request, group string, members []hosted.Member, body []byte, entry activity.Entry) {
	allowed := members
	conv := conversationKey(r, body)
	order := groupOrder(allowed, stickyMember(conv), int64(hosted.EstimatePromptTokens(body)), time.Now())
	if len(order) == 0 {
		writeAPIError(w, http.StatusServiceUnavailable, fmt.Sprintf("every model in group %s is cooling down after errors or quota limits; try again in a minute", group))
		entry.Status = http.StatusServiceUnavailable
		activityRec.record(r, entry)
		return
	}
	for i, m := range order {
		e := entry
		e.Model = group + " → " + m.ID()
		if attemptHosted(w, r, m.Provider, m.Model, group, body, e, i < len(order)-1) {
			rememberMember(conv, m.ID())
			return
		}
	}
}

// groupOrder lists the members to try: the conversation's own member first
// if it still has free allowance, then the others with free allowance, then
// those that would be paid for, each in the group's order. "Free" means the
// allowance left covers at least this request's prompt (need tokens), so a
// nearly spent allowance doesn't take a request it can't hold. Members
// cooling down after an error are left out.
func groupOrder(members []hosted.Member, sticky string, need int64, now time.Time) []hosted.Member {
	var free, paid []hosted.Member
	for _, m := range members {
		if cooling(m.Provider.Name, now) {
			continue
		}
		if exit.spend.FreeLeft(m.Provider, now) >= max(need, 1) {
			if m.ID() == sticky {
				free = append([]hosted.Member{m}, free...)
			} else {
				free = append(free, m)
			}
		} else if m.ID() == sticky {
			paid = append([]hosted.Member{m}, paid...)
		} else {
			paid = append(paid, m)
		}
	}
	return append(free, paid...)
}

var groupState struct {
	sync.Mutex
	cool   map[string]time.Time // provider -> cooling down until
	sticky map[string]string    // conversation -> member
}

func coolProvider(name string, d time.Duration) {
	groupState.Lock()
	defer groupState.Unlock()
	if groupState.cool == nil {
		groupState.cool = map[string]time.Time{}
	}
	groupState.cool[name] = time.Now().Add(d)
}

func cooling(name string, now time.Time) bool {
	groupState.Lock()
	defer groupState.Unlock()
	return now.Before(groupState.cool[name])
}

func stickyMember(conv string) string {
	groupState.Lock()
	defer groupState.Unlock()
	return groupState.sticky[conv]
}

func rememberMember(conv, member string) {
	groupState.Lock()
	defer groupState.Unlock()
	if groupState.sticky == nil || len(groupState.sticky) > 2000 {
		groupState.sticky = map[string]string{}
	}
	groupState.sticky[conv] = member
}

// conversationKey identifies a conversation across turns of a stateless
// API: the app, the system prompt and the first user message, hashed.
func conversationKey(r *http.Request, body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	h := sha256.New()
	h.Write([]byte(r.UserAgent()))
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "user" {
			h.Write([]byte(m.Role))
			h.Write(m.Content)
			if m.Role == "user" {
				break
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// replyCapture keeps the reply for the outbound payload as it streams.
type replyCapture struct {
	*speedMeter
	buf bytes.Buffer
}

func (c *replyCapture) Write(p []byte) (int, error) {
	if room := hosted.MaxPayloadReply + 1 - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return c.speedMeter.Write(p)
}

func describeEntities(counts map[string]int) string {
	if len(counts) == 0 {
		return "nothing"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d %s", counts[k], k)
	}
	return strings.Join(parts, ", ")
}

// showOutbound is `jag outbound [day]` and `jag outbound show <id>`: what
// left the house. Payloads are shown here only, never on the dashboard,
// which anyone on the LAN can open.
func showOutbound(args []string) error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	log, err := hosted.OpenLog(dir)
	if err != nil {
		return err
	}
	if len(args) >= 1 && args[0] == "show" {
		if len(args) != 2 {
			return usagef("usage: jag outbound show <id>")
		}
		p, err := log.ReadPayload(args[1])
		if err != nil {
			return codedError{"not_found", "", fmt.Errorf("no payload for %s (refused requests keep none): %w", args[1], err)}
		}
		if jsonOutput {
			return emitJSON(p)
		}
		raw, _ := json.MarshalIndent(p, "", "  ")
		fmt.Println(string(raw))
		return nil
	}
	day, err := activity.ParseDay(strings.Join(args, ""), time.Now())
	if err != nil {
		return err
	}
	entries, err := log.Day(day)
	if err != nil {
		return err
	}
	if jsonOutput {
		return outboundJSON(dir, day, entries)
	}
	if len(entries) == 0 {
		fmt.Printf("nothing left the house on %s\n", day.Format("2006-01-02"))
	}
	for _, e := range entries {
		what := "SENT   "
		switch {
		case e.Sent && e.Confirmed:
			what = "SENT (confirmed by the person)"
		case !e.Sent && strings.HasPrefix(e.Reason, "held:"):
			what = "HELD   "
		case !e.Sent:
			what = "REFUSED"
		}
		fmt.Printf("%s  %s  %s/%s  %s  %s  %s\n", e.At.Local().Format("15:04:05"), what, e.Provider, e.Model, e.Verdict, describeEntities(e.Entities), e.ID)
		if e.Sent {
			fmt.Printf("          %d bytes out, %d+%d tokens, $%.6f, status %d\n", e.BytesSent, e.PromptTokens, e.CompletionTokens, e.CostUSD, e.Status)
		} else {
			fmt.Printf("          %s\n", e.Reason)
		}
	}
	cfg, err := hosted.Load(dir)
	if err != nil {
		return err
	}
	if spend, err := hosted.OpenLedger(dir); err == nil {
		for _, p := range cfg.Providers {
			u := spend.Used(p.Name)
			fmt.Printf("\n%s: $%.4f of $%.2f spent, %d requests", p.Name, u.USD, p.CapUSD, u.Requests)
			if p.Free != nil {
				fmt.Printf(", %d of %d free tokens left (%s)", spend.FreeLeft(&p, time.Now()), p.Free.Tokens, p.Free.Per)
			}
			fmt.Println()
		}
	}
	return nil
}

// spendJSON is one provider's spending in `jag outbound --json`.
type spendJSON struct {
	Provider   string  `json:"provider"`
	SpentUSD   float64 `json:"spent_usd"`
	CapUSD     float64 `json:"cap_usd"`
	Requests   int     `json:"requests"`
	FreeLeft   *int64  `json:"free_tokens_left,omitempty"`
	FreeTokens int64   `json:"free_tokens,omitempty"`
	FreePer    string  `json:"free_per,omitempty"`
}

func outboundJSON(dir string, day time.Time, entries []hosted.Entry) error {
	if entries == nil {
		entries = []hosted.Entry{}
	}
	cfg, err := hosted.Load(dir)
	if err != nil {
		return err
	}
	spending := []spendJSON{}
	if spend, err := hosted.OpenLedger(dir); err == nil {
		for _, p := range cfg.Providers {
			u := spend.Used(p.Name)
			sj := spendJSON{Provider: p.Name, SpentUSD: u.USD, CapUSD: p.CapUSD, Requests: u.Requests}
			if p.Free != nil {
				left := spend.FreeLeft(&p, time.Now())
				sj.FreeLeft, sj.FreeTokens, sj.FreePer = &left, p.Free.Tokens, p.Free.Per
			}
			spending = append(spending, sj)
		}
	}
	return emitJSON(map[string]any{"day": day.Format("2006-01-02"), "entries": entries, "spending": spending})
}
