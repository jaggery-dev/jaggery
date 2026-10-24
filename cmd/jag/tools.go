package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
)

// Tool use: an app that hands a model tools lets the model act,
// and prompt injection can turn those tools against the house. The router
// sees both halves: the tools a request offers, and the tool calls a reply
// makes. It logs their names only, never arguments, which can carry private
// data just as prompts do.

// toolsOffered lists the function names a chat request offers the model.
func toolsOffered(body []byte) []string {
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	var names []string
	for _, t := range req.Tools {
		if t.Function.Name != "" {
			names = append(names, t.Function.Name)
		}
	}
	return names
}

// toolCallsIn collects the function names a reply chunk (or a whole reply)
// calls: message.tool_calls in a plain reply, delta.tool_calls in a stream,
// where a call's name arrives once, in its first chunk.
func toolCallsIn(raw []byte) []string {
	if !bytes.Contains(raw, []byte(`"tool_calls"`)) {
		return nil
	}
	type call struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []call `json:"tool_calls"`
			} `json:"message"`
			Delta struct {
				ToolCalls []call `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	var names []string
	for _, c := range resp.Choices {
		for _, tc := range append(c.Message.ToolCalls, c.Delta.ToolCalls...) {
			if tc.Function.Name != "" {
				names = append(names, tc.Function.Name)
			}
		}
	}
	return names
}

// toolScanner watches a streamed reply line by line for tool calls.
type toolScanner struct {
	partial []byte
	calls   []string
}

func (s *toolScanner) write(p []byte) {
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(s.partial[:i])
		s.partial = s.partial[i+1:]
		if bytes.HasPrefix(line, []byte("data: {")) {
			s.calls = append(s.calls, toolCallsIn(line[len("data: "):])...)
		}
	}
	if len(s.partial) > 1<<20 { // not a stream of lines; stop holding it
		s.partial = s.partial[:0]
	}
}

// toolCallsOf is what the model called, from a plain reply or a stream.
func toolCallsOf(m *speedMeter) []string {
	if len(m.tools.calls) > 0 {
		return m.tools.calls
	}
	return toolCallsIn(m.head.Bytes())
}

// toolLedger remembers which app has offered which tool, so the first time
// a new one appears it raises an alert: new power handed to a model.
type toolLedger struct {
	path string
	mu   sync.Mutex
	l    struct {
		Seen   map[string]time.Time `json:"seen"` // app + "\x00" + tool
		Events []activity.ToolEvent `json:"events"`
	}
}

var toolSeen *toolLedger

func openToolLedger(stateDir string) *toolLedger {
	t := &toolLedger{path: filepath.Join(stateDir, "tools.json")}
	if raw, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(raw, &t.l)
	}
	if t.l.Seen == nil {
		t.l.Seen = map[string]time.Time{}
	}
	return t
}

// observe records the tools an app offered a model, and returns the ones
// this machine hasn't seen that app offer before.
func (t *toolLedger) observe(e activity.Entry) []activity.ToolEvent {
	if t == nil || len(e.ToolsOffered) == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var fresh []activity.ToolEvent
	for _, name := range e.ToolsOffered {
		k := e.App + "\x00" + name
		if _, ok := t.l.Seen[k]; ok {
			continue
		}
		t.l.Seen[k] = e.At
		fresh = append(fresh, activity.ToolEvent{At: e.At, App: e.App, Tool: name, Model: e.Model, Device: e.Device})
	}
	if len(fresh) == 0 {
		return nil
	}
	t.l.Events = append(t.l.Events, fresh...)
	if over := len(t.l.Events) - 200; over > 0 {
		t.l.Events = t.l.Events[over:]
	}
	if raw, err := json.MarshalIndent(t.l, "", "  "); err == nil {
		_ = os.WriteFile(t.path+".tmp", raw, 0o600)
		_ = os.Rename(t.path+".tmp", t.path)
	}
	return fresh
}

// recent returns the last week's new-tool events, newest first.
func (t *toolLedger) recent(now time.Time) []activity.ToolEvent {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []activity.ToolEvent
	for _, e := range t.l.Events {
		if now.Sub(e.At) < 7*24*time.Hour {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
