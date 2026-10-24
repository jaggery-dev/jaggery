package hosted

import (
	"encoding/json"
	"errors"
)

// Prepare rewrites a request for the provider: its own model name, a reply
// no longer than the provider allows (so the worst case is known before
// sending), and, when streamed, a usage block at the end (so the cost is
// counted, not guessed). It returns the body and the most output tokens the
// reply can have.
func Prepare(body []byte, p *Provider, model string) ([]byte, int, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, 0, err
	}
	if req == nil {
		return nil, 0, errors.New("request is not a JSON object")
	}
	req["model"], _ = json.Marshal(model)

	maxOut := p.MaxOutputTokens
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		raw, ok := req[field]
		if !ok {
			continue
		}
		var n int
		if json.Unmarshal(raw, &n) == nil && n > 0 && n < maxOut {
			maxOut = n
		}
		delete(req, field)
	}
	req["max_tokens"], _ = json.Marshal(maxOut)

	var stream bool
	if raw, ok := req["stream"]; ok {
		_ = json.Unmarshal(raw, &stream)
	}
	if stream {
		req["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	out, err := json.Marshal(req)
	return out, maxOut, err
}

// EstimatePromptTokens is a deliberately high guess at a prompt's tokens,
// for the cap check before sending: a token is rarely under 3 bytes of
// JSON, so bytes/3 errs on the side of refusing.
func EstimatePromptTokens(body []byte) int {
	return len(body)/3 + 1
}
