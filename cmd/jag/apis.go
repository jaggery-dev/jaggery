package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/house"
)

// modelAPI is one kind of model request ai.local routes: where the runtime
// serves it, which runtimes can, and how its errors look to the app.
type modelAPI struct {
	// Name is how the house names it: in /v1/infer/<name> between
	// machines, in inventory Serves, and in the activity log.
	Name string
	Path string
	// Kind limits it to one runtime; "" is any runtime with the model.
	Kind airuntime.Kind
	// Ollama: Ollama's own API, whose replies stream as JSON lines unless
	// "stream": false and whose errors are {"error": "<message>"}.
	Ollama bool
	// Generates: the reply is generated text, so its speed counts for
	// routing and a quiet wait for a cold model gets keep-alive comments.
	Generates bool
}

var (
	apiChat       = modelAPI{Name: "chat", Path: "/v1/chat/completions", Generates: true}
	apiEmbeddings = modelAPI{Name: "embeddings", Path: "/v1/embeddings"}
	ollamaAPIs    = []modelAPI{
		{Name: "ollama/chat", Path: "/api/chat", Kind: airuntime.Ollama, Ollama: true, Generates: true},
		{Name: "ollama/generate", Path: "/api/generate", Kind: airuntime.Ollama, Ollama: true, Generates: true},
		{Name: "ollama/embed", Path: "/api/embed", Kind: airuntime.Ollama, Ollama: true},
		{Name: "ollama/embeddings", Path: "/api/embeddings", Kind: airuntime.Ollama, Ollama: true},
		{Name: "ollama/show", Path: "/api/show", Kind: airuntime.Ollama, Ollama: true},
	}
)

// servedAPIs are the requests this build serves for other machines, beyond
// chat, which every build does. A router only sends a machine what it lists.
func servedAPIs() []string {
	out := []string{apiEmbeddings.Name}
	for _, a := range ollamaAPIs {
		out = append(out, a.Name)
	}
	return out
}

func apiByName(name string) (modelAPI, bool) {
	if name == apiChat.Name {
		return apiChat, true
	}
	if name == apiEmbeddings.Name {
		return apiEmbeddings, true
	}
	for _, a := range ollamaAPIs {
		if a.Name == name {
			return a, true
		}
	}
	return modelAPI{}, false
}

// writeError answers in the API's own error shape, so the app's client
// library shows the message.
func (a modelAPI) writeError(w http.ResponseWriter, status int, msg string) {
	if !a.Ollama {
		writeAPIError(w, status, msg)
		return
	}
	writeOllamaError(w, status, msg)
}

func writeOllamaError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// streamed reports whether the reply to body will stream. Ollama's own API
// streams unless told not to; OpenAI's only when asked.
func (a modelAPI) streamed(body []byte) bool {
	var req struct {
		Stream *bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	if req.Stream != nil {
		return *req.Stream
	}
	return a.Ollama && a.Generates
}

// model is the model a request names. Ollama's older requests say "name".
func (a modelAPI) model(body []byte) string {
	var req struct {
		Model string `json:"model"`
		Name  string `json:"name"`
	}
	_ = json.Unmarshal(body, &req)
	if req.Model == "" && a.Ollama {
		return req.Name
	}
	return req.Model
}

// canServe narrows machines to what can serve a: for an Ollama API only
// their Ollama runtimes count, and a peer must say it serves a.
func (a modelAPI) canServe(machines []house.Machine) []house.Machine {
	out := make([]house.Machine, 0, len(machines))
	for _, m := range machines {
		if a.Name != apiChat.Name && !m.Self && !contains(m.Inventory.Serves, a.Name) {
			continue
		}
		if a.Kind != "" {
			var kept []airuntime.Runtime
			for _, rt := range m.Inventory.Runtimes {
				if rt.Kind == a.Kind {
					kept = append(kept, rt)
				}
			}
			m.Inventory.Runtimes = kept
		}
		out = append(out, m)
	}
	return out
}

// whyNot explains, for a model some machine has but none can serve through
// a, what to do instead.
func (a modelAPI) whyNot(machines []house.Machine, model string) (string, bool) {
	for _, m := range machines {
		for _, rt := range m.Inventory.Runtimes {
			for _, mo := range rt.Models {
				if mo.Name != model && mo.Name != model+":latest" {
					continue
				}
				if a.Kind != "" && rt.Kind != a.Kind {
					return fmt.Sprintf("model %q is in %s on %s, not in Ollama: use it through ai.local/v1 instead", model, airuntime.Names[rt.Kind], m.Name), true
				}
				if !m.Self && !contains(m.Inventory.Serves, a.Name) {
					return fmt.Sprintf("model %q is on %s, which runs an older Jaggery that can't serve this request: update it with `jag update` there", model, m.Name), true
				}
			}
		}
	}
	return "", false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
