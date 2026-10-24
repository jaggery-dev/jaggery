// Package activity keeps the AI activity log: one line per request through
// the router, saying which device and app asked which model, which machine
// served it, how many tokens and how long it took.
//
// It records metadata only, never prompts or replies. Each agent logs the
// requests it routed, in a file per day, and keeps Retention days of them;
// the dashboard gathers the house's from every machine.
package activity

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	dirName   = "activity"
	Retention = 30 * 24 * time.Hour
	dayLayout = "2006-01-02"
)

// Entry is one request through the router.
type Entry struct {
	At time.Time `json:"at"`
	// Device is who asked: a machine in the house, a phone's .local name, or
	// an address when nothing answered to it.
	Device string `json:"device"`
	Addr   string `json:"addr"`
	// App is the client, from its User-Agent; User is the person, when the
	// app says (Open WebUI can forward it).
	App   string `json:"app,omitempty"`
	User  string `json:"user,omitempty"`
	Model string `json:"model"`
	// ServedBy is the machine whose runtime answered; empty if none could.
	ServedBy string `json:"served_by,omitempty"`
	Status   int    `json:"status"`
	Stream   bool   `json:"stream,omitempty"`
	// Tokens are the runtime's counts where it reports them. A streamed
	// reply without a usage block counts its chunks, about one token each,
	// and is marked Estimated.
	PromptTokens     int   `json:"prompt_tokens,omitempty"`
	CompletionTokens int   `json:"completion_tokens,omitempty"`
	Estimated        bool  `json:"estimated,omitempty"`
	DurationMS       int64 `json:"duration_ms"`
	// ToolsOffered are the function names the app gave the model; ToolCalls
	// the ones the model called. Names only: arguments can
	// carry private data, so they are never logged.
	ToolsOffered []string `json:"tools_offered,omitempty"`
	ToolCalls    []string `json:"tool_calls,omitempty"`
	// RoutedBy is the machine that logged it, filled in when the house's
	// logs are gathered.
	RoutedBy string `json:"routed_by,omitempty"`
}

// ToolEvent is the first time an app offered a model a given tool.
type ToolEvent struct {
	At     time.Time `json:"at"`
	App    string    `json:"app"`
	Tool   string    `json:"tool"`
	Model  string    `json:"model"`
	Device string    `json:"device"`
}

// Log appends entries to a file per day under dir.
type Log struct {
	dir string
	mu  sync.Mutex
}

func Open(stateDir string) (*Log, error) {
	dir := filepath.Join(stateDir, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Log{dir: dir}, nil
}

func (l *Log) path(day time.Time) string {
	return filepath.Join(l.dir, day.Local().Format(dayLayout)+".jsonl")
}

// Append records e in its day's file.
func (l *Log) Append(e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path(e.At), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// Day returns the entries logged on day (local time), oldest first.
func (l *Log) Day(day time.Time) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path(day))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Prune deletes days older than Retention.
func (l *Log) Prune(now time.Time) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-Retention)
	for _, e := range entries {
		day, err := time.ParseInLocation(dayLayout, strings.TrimSuffix(e.Name(), ".jsonl"), time.Local)
		if err == nil && day.Before(cutoff) {
			_ = os.Remove(filepath.Join(l.dir, e.Name()))
		}
	}
}

// ParseDay reads "2026-10-07", or "" for today.
func ParseDay(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return now, nil
	}
	day, err := time.ParseInLocation(dayLayout, s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("day must look like 2026-10-07")
	}
	return day, nil
}

// Merge combines entries from several machines, newest first.
func Merge(lists ...[]Entry) []Entry {
	out := []Entry{} // a quiet day is an empty list, not null
	for _, l := range lists {
		out = append(out, l...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// AppName turns a User-Agent into the name of the app behind it, where we
// know the pattern; otherwise the User-Agent's first word.
func AppName(userAgent string) string {
	ua := strings.ToLower(userAgent)
	known := []struct{ marker, name string }{
		{"open-webui", "Open WebUI"},
		{"openwebui", "Open WebUI"},
		{"goose", "Goose"},
		{"continue", "Continue"},
		{"cline", "Cline"},
		{"n8n", "n8n"},
		{"home assistant", "Home Assistant"},
		{"homeassistant", "Home Assistant"},
		{"langchain", "LangChain"},
		{"openai/python", "OpenAI Python SDK"},
		{"openai/js", "OpenAI JS SDK"},
		{"aiohttp", "Python (aiohttp)"},
		{"python-requests", "Python (requests)"},
		{"curl", "curl"},
		{"mozilla", "browser"},
	}
	for _, k := range known {
		if strings.Contains(ua, k.marker) {
			return k.name
		}
	}
	if f := strings.Fields(userAgent); len(f) > 0 {
		name := f[0]
		if len(name) > 40 {
			name = name[:40]
		}
		return name
	}
	return ""
}
