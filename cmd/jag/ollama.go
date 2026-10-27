package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/version"
)

// Ollama's own API on ai.local, for apps that speak only that (Ollama
// connections in Open WebUI, Home Assistant, Obsidian plugins). Requests
// that use a model are routed like chat, to a machine whose Ollama has the
// model. The lists are the house's: every approved Ollama model on every
// machine. Requests that change models (pull, create, delete, …) are
// refused: they stay at a terminal on the machine, by a person.

func registerOllama(mux *http.ServeMux, route func(http.ResponseWriter, *http.Request, modelAPI)) {
	for _, api := range ollamaAPIs {
		mux.HandleFunc("POST "+api.Path, func(w http.ResponseWriter, r *http.Request) { route(w, r, api) })
	}
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"models": ollamaModels(false)})
	})
	mux.HandleFunc("GET /api/ps", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"models": ollamaModels(true)})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": ollamaVersion(r.Context())})
	})
	for _, route := range []string{"POST /api/pull", "POST /api/push", "POST /api/create", "POST /api/copy", "DELETE /api/delete", "POST /api/blobs/{digest}", "HEAD /api/blobs/{digest}"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			writeOllamaError(w, http.StatusForbidden, "ai.local doesn't change models: run ollama on the machine that should have it, for example `ollama pull <model>` there, then approve it with `jag models approve <model>`")
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeOllamaError(w, http.StatusNotFound, r.Method+" "+r.URL.Path+" isn't served by ai.local (see docs/how-to/use-ai-local.md)")
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ollamaModel is one entry of /api/tags or /api/ps, in Ollama's shape.
type ollamaModel struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Size       uint64 `json:"size"`
	Digest     string `json:"digest"`
	Details    struct {
		Format            string `json:"format,omitempty"`
		Family            string `json:"family,omitempty"`
		ParameterSize     string `json:"parameter_size,omitempty"`
		QuantizationLevel string `json:"quantization_level,omitempty"`
	} `json:"details"`
	SizeVRAM uint64 `json:"size_vram,omitempty"`
}

// ollamaModels lists the approved models in the house's Ollamas, each name
// once; loaded keeps only those loaded somewhere.
func ollamaModels(loaded bool) []ollamaModel {
	byName := map[string]ollamaModel{}
	for _, m := range apiOllamaChat().canServe(approvedOnly(loadHouse().All(), approvalList)) {
		if !m.Self && (!m.Reachable || m.Incompatible) {
			continue
		}
		for _, rt := range m.Inventory.Runtimes {
			for _, mo := range rt.Models {
				if loaded && !mo.Loaded {
					continue
				}
				if prev, ok := byName[mo.Name]; ok && (prev.SizeVRAM > 0 || !mo.Loaded) {
					continue
				}
				om := ollamaModel{Name: mo.Name, Model: mo.Name, Size: mo.SizeBytes, Digest: mo.Digest}
				if mo.FirstSeen != nil {
					om.ModifiedAt = mo.FirstSeen.Format(time.RFC3339)
				}
				om.Details.Format, om.Details.Family = mo.Format, mo.Family
				om.Details.ParameterSize, om.Details.QuantizationLevel = mo.Parameters, mo.Quantization
				if mo.Loaded {
					om.SizeVRAM = mo.VRAMBytes
				}
				byName[mo.Name] = om
			}
		}
	}
	out := make([]ollamaModel, 0, len(byName))
	for _, om := range byName {
		out = append(out, om)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func apiOllamaChat() modelAPI {
	a, _ := apiByName("ollama/chat")
	return a
}

// ollamaVersion is this machine's Ollama's version, so apps that check it
// see a real one; without a local Ollama, Jaggery's own.
func ollamaVersion(ctx context.Context) string {
	for _, rt := range airuntime.Detect(ctx) {
		if rt.Kind != airuntime.Ollama {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rt.Endpoint+"/api/version", nil)
		if err != nil {
			break
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			break
		}
		var v struct {
			Version string `json:"version"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v)
		resp.Body.Close()
		if err == nil && v.Version != "" {
			return v.Version
		}
		break
	}
	return version.Version
}

// ollamaUsage is the counts Ollama puts in a reply's last object.
type ollamaUsage struct {
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
	EvalDuration    int64 `json:"eval_duration"` // nanoseconds
	Done            bool  `json:"done"`
}

// tokensPerSec is Ollama's own measure of generation speed, which leaves
// out loading and the prompt, so it counts whether the model was loaded.
func (u ollamaUsage) tokensPerSec() (float64, bool) {
	if u.EvalCount < 2 || u.EvalDuration <= 0 {
		return 0, false
	}
	return float64(u.EvalCount) / (float64(u.EvalDuration) / 1e9), true
}

// ollamaUsageOf finds the final {"done": true, …} object of a reply, the
// whole body when not streamed or the last line of a stream.
func ollamaUsageOf(m *speedMeter) (ollamaUsage, bool) {
	var u ollamaUsage
	if json.Unmarshal(bytes.TrimSpace(m.head.Bytes()), &u) == nil && u.Done {
		return u, true
	}
	lines := bytes.Split(bytes.TrimSpace(m.tail.Bytes()), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var u ollamaUsage
		if json.Unmarshal(bytes.TrimSpace(lines[i]), &u) == nil && u.Done {
			return u, true
		}
	}
	return ollamaUsage{}, false
}
