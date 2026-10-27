package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/router"
)

// registerRouter adds the OpenAI-compatible AI endpoint to the LAN-facing mux.
// This is the hook: point any OpenAI client at http://<machine>:7373/v1 and it
// reaches whichever machine in the house can serve the model. Open to the LAN
// by design, like the README says.
func registerRouter(mux *http.ServeMux, id *identity.Identity, store *home.Store) {
	speeds := router.NewSpeedBook()

	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		snap := loadHouse()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   append(router.MergedModels(approvedOnly(snap.All(), approvalList)), hostedModels()...),
		})
	})

	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, ok := readModelRequest(w, r, apiChat)
		if !ok {
			return
		}
		model := apiChat.model(body)
		entry := activity.Entry{At: time.Now().UTC(), Model: model, Stream: apiChat.streamed(body), ToolsOffered: toolsOffered(body)}

		// "<provider>/<model>" goes out of the house, through the gate.
		providers := hostedConfig()
		if members, ok := providers.Group(model); ok {
			serveGroup(w, r, model, members, body, entry)
			return
		}
		if p, bare, ok := providers.Find(model); ok {
			serveHosted(w, r, p, bare, body, entry)
			return
		}
		if p, ok := providers.Named(model); ok {
			writeAPIError(w, http.StatusForbidden, fmt.Sprintf("%s is not one of the models allowed at %s (%s)", model, p.Name, strings.Join(p.Models, ", ")))
			entry.Status = http.StatusForbidden
			activityRec.record(r, entry)
			return
		}
		routeModel(w, r, apiChat, body, entry, speeds, id, store)
	})

	mux.HandleFunc("POST /v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		body, ok := readModelRequest(w, r, apiEmbeddings)
		if !ok {
			return
		}
		model := apiEmbeddings.model(body)
		entry := activity.Entry{At: time.Now().UTC(), Model: model, API: apiEmbeddings.Name}
		if _, _, ok := hostedConfig().Find(model); ok {
			writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("%s is a hosted model: ai.local sends only chat to hosted providers so far; use a model from the house for embeddings", model))
			entry.Status = http.StatusBadRequest
			activityRec.record(r, entry)
			return
		}
		routeModel(w, r, apiEmbeddings, body, entry, speeds, id, store)
	})

	registerOllama(mux, func(w http.ResponseWriter, r *http.Request, api modelAPI) {
		body, ok := readModelRequest(w, r, api)
		if !ok {
			return
		}
		entry := activity.Entry{At: time.Now().UTC(), Model: api.model(body), API: api.Name, Stream: api.streamed(body)}
		if api.Name == "ollama/chat" {
			entry.ToolsOffered = toolsOffered(body)
		}
		routeModel(w, r, api, body, entry, speeds, id, store)
	})
}

// readModelRequest reads a model request's body; false when it has already
// answered with an error.
func readModelRequest(w http.ResponseWriter, r *http.Request, api modelAPI) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		api.writeError(w, http.StatusBadRequest, "reading request")
		return nil, false
	}
	if api.model(body) == "" {
		api.writeError(w, http.StatusBadRequest, "missing model")
		return nil, false
	}
	return body, true
}

// routeModel sends a request for one of the house's own models to the
// machine that should serve it, streams the reply back and logs it.
func routeModel(w http.ResponseWriter, r *http.Request, api modelAPI, body []byte, entry activity.Entry, speeds *router.SpeedBook, id *identity.Identity, store *home.Store) {
	model := entry.Model
	snap := loadHouse()
	machines := api.canServe(approvedOnly(withLivePause(snap.All()), approvalList))
	entry.Paused = router.PausedWith(machines, model)
	target, ok := router.Pick(machines, model, speeds)
	if !ok {
		status, msg := http.StatusNotFound, fmt.Sprintf("no machine in the house has model %q", model)
		if len(entry.Paused) > 0 {
			status = http.StatusServiceUnavailable
			msg = fmt.Sprintf("model %q is only on %s, where AI requests are paused: try again later, or run `jag ai resume` there", model, strings.Join(entry.Paused, ", "))
		} else if _, has := router.Pick(api.canServe(snap.All()), model, speeds); has {
			status = http.StatusForbidden
			msg = fmt.Sprintf("model %q is waiting for approval: run `jag models approve %s` on any machine in the house", model, model)
		} else if why, ok := api.whyNot(approvedOnly(snap.All(), approvalList), model); ok {
			msg = why
		}
		api.writeError(w, status, msg)
		entry.Status = status
		activityRec.record(r, entry)
		return
	}

	out := w
	var waiting *waitingStream
	if api.Generates && !api.Ollama && entry.Stream && !target.Loaded {
		waiting = newWaitingStream(w, fmt.Sprintf("loading %s on %s; the first reply can take a few minutes", model, target.Name))
		out = waiting
	}
	meter := newSpeedMeter(out)
	var err error
	if target.Self {
		err = serveLocal(r.Context(), meter, api, model, body)
	} else {
		err = servePeer(r.Context(), meter, id, store, target.MachineID, api, body)
	}
	if waiting != nil {
		waiting.finish(err)
	}
	entry.ServedBy = machineName(snap, target.MachineID)
	entry.DurationMS = time.Since(meter.start).Milliseconds()
	if err != nil {
		if waiting == nil {
			api.writeError(w, http.StatusBadGateway, err.Error())
		}
		entry.Status = http.StatusBadGateway
		activityRec.record(r, entry)
		return
	}
	entry.Status = meter.status
	if entry.Status == 0 {
		entry.Status = http.StatusOK
	}
	if !api.Generates {
		activityRec.record(r, entry)
		return
	}
	if api.Ollama {
		u, ok := ollamaUsageOf(meter)
		if ok {
			entry.PromptTokens, entry.CompletionTokens = u.PromptEvalCount, u.EvalCount
		}
		activityRec.record(r, entry)
		if tps, ok := u.tokensPerSec(); ok && meter.status == http.StatusOK {
			speeds.Record(target.MachineID, model, tps)
		}
		return
	}
	entry.PromptTokens, entry.CompletionTokens, entry.Estimated = usageOf(meter)
	entry.ToolCalls = toolCallsOf(meter)
	activityRec.record(r, entry)
	if tps, ok := meter.tokensPerSec(target.Loaded); ok {
		speeds.Record(target.MachineID, model, tps)
	}
}

// serveLocal proxies a model request to a runtime on this machine, streaming
// the response straight back to the client.
func serveLocal(ctx context.Context, w http.ResponseWriter, api modelAPI, model string, body []byte) error {
	runtimes := airuntime.Detect(ctx)
	if api.Kind != "" {
		var kept []airuntime.Runtime
		for _, rt := range runtimes {
			if rt.Kind == api.Kind {
				kept = append(kept, rt)
			}
		}
		runtimes = kept
	}
	endpoint, mo, ok := router.LocalModelFor(runtimes, model)
	if !ok {
		return fmt.Errorf("model %q is not on this machine after all", model)
	}
	// Checked again where it runs: the files may have changed since the
	// router last saw this machine's inventory.
	if approvalList != nil && !approvalList.Approved(mo.Name, mo.Digest) {
		return fmt.Errorf("model %q here isn't the approved one (its files changed); approve it with `jag models approve %s`", model, mo.Name)
	}
	if !mo.Loaded {
		done := loadingNow.begin(mo.Name)
		defer done()
		w = firstByte{w, done}
	}
	return proxyStream(ctx, w, endpoint+api.Path, body)
}

// servePeer forwards a model request to a peer over mutual TLS; the peer
// serves it from its own local runtime and streams back. Chat goes to
// /v1/infer, which every build serves; the rest to /v1/infer/<api>.
func servePeer(ctx context.Context, w http.ResponseWriter, id *identity.Identity, store *home.Store, machineID string, api modelAPI, body []byte) error {
	peer, err := store.Get(machineID)
	if err != nil {
		return err
	}
	if peer.Trust != home.Approved || !peer.Verified() || len(peer.Addrs) == 0 {
		return fmt.Errorf("%s is not a reachable approved peer", peer.Name)
	}

	client, err := peerHTTPClient(id, ed25519.PublicKey(peer.PublicKey), 0)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(peer.Addrs[0].String(), strconv.Itoa(discovery.SecurePort))
	path := "/v1/infer"
	if api.Name != apiChat.Name {
		path += "/" + api.Name
	}
	return proxyStreamWith(ctx, client, w, "https://"+addr+path, body)
}

// serveInfer is the peer end of servePeer: an approved machine asks us to run a
// model, we serve it from our own local runtime. Lives on the secured listener.
func serveInfer(ctx context.Context, w http.ResponseWriter, api modelAPI, body []byte) error {
	model := api.model(body)
	if model == "" {
		return fmt.Errorf("missing model")
	}
	return serveLocal(ctx, w, api, model, body)
}

func proxyStream(ctx context.Context, w http.ResponseWriter, url string, body []byte) error {
	return proxyStreamWith(ctx, http.DefaultClient, w, url, body)
}

// proxyStreamWith forwards the body to url and streams the response back.
func proxyStreamWith(ctx context.Context, client *http.Client, w http.ResponseWriter, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)

	// From here the status is sent, so a failure can only cut the stream
	// short; returning nil stops the caller writing an error into the body.
	if err := copyFlushing(w, resp.Body); err != nil {
		fmt.Fprintln(os.Stderr, "warning: ai stream cut short:", err)
	}
	return nil
}

// copyFlushing copies src to w, flushing as chunks arrive so token streaming
// reaches the caller live.
func copyFlushing(w http.ResponseWriter, src io.Reader) error {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// speedMeter watches a completion pass through to the client and estimates
// how fast the serving machine generated it, which is what routing ranks by.
type speedMeter struct {
	http.ResponseWriter
	status             int
	start, first, last time.Time
	events             int          // streamed chunks, about one token each
	head               bytes.Buffer // the start of the body, for a non-streamed usage block
	tail               bytes.Buffer // the end of a stream, where a usage block arrives
	tools              toolScanner  // tool calls in a stream
}

const meterTail = 8 << 10

const meterHead = 64 << 10

func newSpeedMeter(w http.ResponseWriter) *speedMeter {
	return &speedMeter{ResponseWriter: w, start: time.Now()}
}

func (m *speedMeter) WriteHeader(code int) {
	m.status = code
	m.ResponseWriter.WriteHeader(code)
}

func (m *speedMeter) Write(p []byte) (int, error) {
	now := time.Now()
	if m.first.IsZero() && len(p) > 0 {
		m.first = now
	}
	m.last = now
	m.events += bytes.Count(p, []byte("data: {"))
	if room := meterHead - m.head.Len(); room > 0 {
		m.head.Write(p[:min(len(p), room)])
	}
	m.tail.Write(p)
	if over := m.tail.Len() - meterTail; over > 0 {
		m.tail.Next(over)
	}
	m.tools.write(p)
	return m.ResponseWriter.Write(p)
}

func (m *speedMeter) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// tokensPerSec is the observed generation rate. Streamed, it is chunks over
// the time between the first and last chunk, which leaves out model loading
// and prompt processing. Not streamed, only the total time is known, so the
// sample counts only if the model was already loaded; otherwise load time
// would make a fast machine look slow.
func (m *speedMeter) tokensPerSec(loaded bool) (float64, bool) {
	if m.status != 0 && m.status != http.StatusOK {
		return 0, false
	}
	if m.events >= 3 {
		if span := m.last.Sub(m.first).Seconds(); span > 0 {
			return float64(m.events-1) / span, true
		}
		return 0, false
	}
	if !loaded {
		return 0, false
	}
	var resp struct {
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(m.head.Bytes(), &resp) != nil || resp.Usage.CompletionTokens == 0 {
		return 0, false
	}
	if span := m.last.Sub(m.start).Seconds(); span > 0 {
		return float64(resp.Usage.CompletionTokens) / span, true
	}
	return 0, false
}

// writeAPIError answers in the OpenAI error shape, so client libraries surface
// the message instead of failing to parse the body.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": msg, "type": "jaggery_error"},
	})
}

func machineName(snap house.Snapshot, machineID string) string {
	for _, m := range snap.All() {
		if m.MachineID == machineID {
			return m.Name
		}
	}
	return machineID
}

// loadHouse reads the current snapshot; an empty one is fine for an agent that
// just started.
func loadHouse() house.Snapshot {
	dir, err := identity.StateDir()
	if err != nil {
		return house.Snapshot{}
	}
	snap, err := house.Load(dir)
	if err != nil {
		return house.Snapshot{UpdatedAt: time.Now().UTC()}
	}
	return snap
}
