// Package airuntime detects local AI model servers — Ollama, LM Studio,
// llama.cpp, vLLM — by probing their default ports on localhost, plus any
// the owner lists in runtimes.json (config.go), and reports which
// models each has and which are loaded. This is what the AI router (step 7)
// routes against: it needs to know which machine can serve a given model.
//
// Probing is deliberately defensive. Default ports collide with ordinary web
// apps (llama.cpp's 8080 is a common one), so a runtime counts as present only
// if its endpoint answers in the exact shape that runtime uses — never just
// because something returned 200.
package airuntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Kind string

const (
	Ollama   Kind = "ollama"
	LMStudio Kind = "lmstudio"
	LlamaCpp Kind = "llamacpp"
	VLLM     Kind = "vllm"
)

type Model struct {
	Name   string `json:"name"`
	Loaded bool   `json:"loaded"`
	// SizeBytes is the model's size where the runtime says (Ollama's
	// /api/tags); roughly the memory it needs to load. Zero when unknown.
	SizeBytes uint64 `json:"size_bytes,omitempty"`
	// VRAMBytes is how much GPU memory a loaded model holds, where the
	// runtime says (Ollama's size_vram); zero when unloaded or unknown.
	VRAMBytes uint64 `json:"vram_bytes,omitempty"`

	// The model's fingerprint and make-up, where the runtime says (Ollama).
	// Digest identifies the exact files: the same name with a new digest is
	// a different model.
	Digest       string   `json:"digest,omitempty"`
	Format       string   `json:"format,omitempty"` // "gguf", "safetensors", …
	Family       string   `json:"family,omitempty"`
	Parameters   string   `json:"parameters,omitempty"`   // "3.2B"
	Quantization string   `json:"quantization,omitempty"` // "Q4_K_M"
	Capabilities []string `json:"capabilities,omitempty"` // "completion", "tools", "vision", …
	// FirstSeen is when this machine first saw this exact model; set by the
	// running agent from its model ledger.
	FirstSeen *time.Time `json:"first_seen,omitempty"`
	// Pending: the house hasn't approved this exact model, so the router
	// won't serve it. Set by the running agent.
	Pending bool `json:"pending,omitempty"`
}

type Runtime struct {
	Kind     Kind    `json:"kind"`
	Endpoint string  `json:"endpoint"`
	Models   []Model `json:"models"`
}

// DefaultPorts is where each runtime listens unless told otherwise.
var DefaultPorts = map[Kind]int{
	Ollama:   11434,
	LMStudio: 1234,
	LlamaCpp: 8080,
	VLLM:     8000,
}

// Kinds is every runtime Jaggery knows, in a stable order.
var Kinds = []Kind{Ollama, LMStudio, LlamaCpp, VLLM}

// Names are how each kind is written for people.
var Names = map[Kind]string{
	Ollama:   "Ollama",
	LMStudio: "LM Studio",
	LlamaCpp: "llama.cpp",
	VLLM:     "vLLM",
}

var detectors = map[Kind]func(ctx context.Context, c *http.Client, baseURL string) ([]Model, bool){
	Ollama:   detectOllama,
	LMStudio: detectOpenAI,
	LlamaCpp: detectOpenAI,
	VLLM:     detectVLLM,
}

// Port is one place to look for a runtime of a kind.
type Port struct {
	Kind Kind `json:"kind"`
	Port int  `json:"port"`
}

// Detect probes every known runtime on localhost, on its default port and
// on the ports in runtimes.json, concurrently, and returns those present.
func Detect(ctx context.Context) []Runtime {
	return DetectPorts(ctx, "127.0.0.1", Ports())
}

// DetectAt probes the runtimes in ports on host. Besides localhost, it is how
// a peer checks whether a machine's runtimes answer the whole network.
func DetectAt(ctx context.Context, host string, ports map[Kind]int) []Runtime {
	list := make([]Port, 0, len(ports))
	for kind, port := range ports {
		list = append(list, Port{kind, port})
	}
	return DetectPorts(ctx, host, list)
}

// DetectPorts probes each kind on its port on host. A port is probed once:
// where two entries name the same port, the first wins, so an owner's entry
// placed first overrides a default.
func DetectPorts(ctx context.Context, host string, ports []Port) []Runtime {
	client := &http.Client{Timeout: 2 * time.Second}

	var (
		mu       sync.Mutex
		runtimes []Runtime
		wg       sync.WaitGroup
		seen     = map[int]bool{}
	)
	for _, p := range ports {
		detect, ok := detectors[p.Kind]
		if !ok || seen[p.Port] {
			continue
		}
		seen[p.Port] = true
		kind := p.Kind
		baseURL := "http://" + net.JoinHostPort(host, strconv.Itoa(p.Port))
		wg.Add(1)
		go func() {
			defer wg.Done()
			models, ok := detect(ctx, client, baseURL)
			if !ok {
				return
			}
			mu.Lock()
			runtimes = append(runtimes, Runtime{Kind: kind, Endpoint: baseURL, Models: models})
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.Slice(runtimes, func(i, j int) bool {
		if runtimes[i].Kind != runtimes[j].Kind {
			return runtimes[i].Kind < runtimes[j].Kind
		}
		return runtimes[i].Endpoint < runtimes[j].Endpoint
	})
	return runtimes
}

func getJSON(ctx context.Context, c *http.Client, url string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	return body, true
}

// detectOllama reads /api/tags for the model list and /api/ps for which are
// currently loaded into memory.
func detectOllama(ctx context.Context, c *http.Client, baseURL string) ([]Model, bool) {
	tags, ok := getJSON(ctx, c, baseURL+"/api/tags")
	if !ok {
		return nil, false
	}
	models, ok := parseOllamaTags(tags)
	if !ok {
		return nil, false
	}

	if ps, ok := getJSON(ctx, c, baseURL+"/api/ps"); ok {
		loaded := parseOllamaLoaded(ps)
		for i := range models {
			if vram, ok := loaded[models[i].Name]; ok {
				models[i].Loaded = true
				models[i].VRAMBytes = vram
			}
		}
	}
	return models, true
}

func parseOllamaTags(body []byte) ([]Model, bool) {
	var payload struct {
		// A pointer so we can tell an absent "models" key (not Ollama) from a
		// present-but-empty one (Ollama with no models pulled yet).
		Models *[]struct {
			Name    string `json:"name"`
			Size    uint64 `json:"size"`
			Digest  string `json:"digest"`
			Details struct {
				Format            string `json:"format"`
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Models == nil {
		return nil, false
	}
	var out []Model
	for _, m := range *payload.Models {
		if m.Name != "" {
			out = append(out, Model{
				Name:         m.Name,
				SizeBytes:    m.Size,
				Digest:       m.Digest,
				Format:       m.Details.Format,
				Family:       m.Details.Family,
				Parameters:   m.Details.ParameterSize,
				Quantization: m.Details.QuantizationLevel,
				Capabilities: m.Capabilities,
			})
		}
	}
	return out, true
}

// parseOllamaLoaded maps each loaded model to the GPU memory it holds.
func parseOllamaLoaded(body []byte) map[string]uint64 {
	var payload struct {
		Models []struct {
			Name     string `json:"name"`
			SizeVRAM uint64 `json:"size_vram"`
		} `json:"models"`
	}
	loaded := map[string]uint64{}
	if json.Unmarshal(body, &payload) != nil {
		return loaded
	}
	for _, m := range payload.Models {
		loaded[m.Name] = m.SizeVRAM
	}
	return loaded
}

// detectOpenAI reads an OpenAI-compatible /v1/models list (LM Studio,
// llama.cpp). It requires the exact {"object":"list","data":[{"id":...}]}
// shape, so an unrelated web app on the same port is not mistaken for a
// runtime.
func detectOpenAI(ctx context.Context, c *http.Client, baseURL string) ([]Model, bool) {
	body, ok := getJSON(ctx, c, baseURL+"/v1/models")
	if !ok {
		return nil, false
	}
	return parseOpenAIModels(body)
}

func parseOpenAIModels(body []byte) ([]Model, bool) {
	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	// The defining shape: a "list" object with a data array. A plain 200 from
	// some other app will not have this.
	if payload.Object != "list" || payload.Data == nil {
		return nil, false
	}
	var out []Model
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, Model{Name: m.ID})
		}
	}
	return out, true
}

// detectVLLM reads vLLM's /v1/models. The shape is OpenAI's, and its
// default port 8000 is also common for other apps (Paperless-ngx), so it
// counts only if the models say they are vLLM's ("owned_by": "vllm").
func detectVLLM(ctx context.Context, c *http.Client, baseURL string) ([]Model, bool) {
	body, ok := getJSON(ctx, c, baseURL+"/v1/models")
	if !ok {
		return nil, false
	}
	return parseVLLMModels(body)
}

func parseVLLMModels(body []byte) ([]Model, bool) {
	models, ok := parseOpenAIModels(body)
	if !ok {
		return nil, false
	}
	var payload struct {
		Data []struct {
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil, false
	}
	for _, m := range payload.Data {
		if m.OwnedBy == "vllm" {
			return models, true
		}
	}
	return nil, false
}

// Summary is a short human description, e.g. "ollama: 3 models (1 loaded)".
func Summary(runtimes []Runtime) string {
	if len(runtimes) == 0 {
		return "no AI runtime detected"
	}
	parts := make([]string, 0, len(runtimes))
	for _, rt := range runtimes {
		loaded := 0
		for _, m := range rt.Models {
			if m.Loaded {
				loaded++
			}
		}
		parts = append(parts, fmt.Sprintf("%s: %d models (%d loaded)", rt.Kind, len(rt.Models), loaded))
	}
	return join(parts, "; ")
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
