// Package modelwatch keeps a machine's model ledger: every model its AI
// runtimes have, by digest, with when it was first seen, and the events when
// one appears, changes or goes.
//
// A model file is inert until a runtime loads it, but what loads it decides
// how safe that is. GGUF and safetensors are plain tensors; PyTorch's pickle
// formats (.bin, .pt, .pth, .ckpt) can run arbitrary code when loaded, so a
// model in one of those is flagged. A known name whose digest changes is a
// different model under the same name, which is worth an alert too.
package modelwatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
)

const (
	fileName  = "models.json"
	maxEvents = 200
	// EventWindow is how long events stay on the dashboard.
	EventWindow = 7 * 24 * time.Hour
)

// Record is one model as this machine last saw it.
type Record struct {
	Runtime   airuntime.Kind `json:"runtime"`
	Name      string         `json:"name"`
	Digest    string         `json:"digest,omitempty"`
	SizeBytes uint64         `json:"size_bytes,omitempty"`
	Format    string         `json:"format,omitempty"`
	FirstSeen time.Time      `json:"first_seen"`
	LastSeen  time.Time      `json:"last_seen"`
}

type EventKind string

const (
	New     EventKind = "new"
	Changed EventKind = "changed" // same name, different digest
	Removed EventKind = "removed"
)

// Event is a change in what models a machine has.
type Event struct {
	At         time.Time      `json:"at"`
	Kind       EventKind      `json:"kind"`
	Runtime    airuntime.Kind `json:"runtime"`
	Name       string         `json:"name"`
	Digest     string         `json:"digest,omitempty"`
	PrevDigest string         `json:"prev_digest,omitempty"`
	Format     string         `json:"format,omitempty"`
	// Risky marks a format that can run code when loaded.
	Risky bool `json:"risky,omitempty"`
}

type ledger struct {
	// Baselined is set after the first look: models already present when
	// Jaggery first runs are recorded without raising alerts for each.
	Baselined bool              `json:"baselined"`
	Records   map[string]Record `json:"records"`
	Events    []Event           `json:"events"`
}

// Ledger is the on-disk ledger. Only the running agent writes it.
type Ledger struct {
	path string
	mu   sync.Mutex
	l    ledger
}

func Open(stateDir string) *Ledger {
	path := filepath.Join(stateDir, fileName)
	lg := &Ledger{path: path, l: ledger{Records: map[string]Record{}}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &lg.l)
		if lg.l.Records == nil {
			lg.l.Records = map[string]Record{}
		}
	}
	return lg
}

func key(kind airuntime.Kind, name string) string { return string(kind) + "/" + name }

// Observe folds in what the runtimes report now, and returns the events it
// raised. A runtime that isn't running says nothing about its models, so
// models are only marked removed from runtimes that answered.
func (lg *Ledger) Observe(runtimes []airuntime.Runtime, now time.Time) ([]Event, error) {
	lg.mu.Lock()
	defer lg.mu.Unlock()

	var events []Event
	raise := func(e Event) {
		if lg.l.Baselined {
			events = append(events, e)
		}
	}
	seen := map[string]bool{}
	answered := map[airuntime.Kind]bool{}
	for _, rt := range runtimes {
		answered[rt.Kind] = true
		for _, m := range rt.Models {
			k := key(rt.Kind, m.Name)
			seen[k] = true
			rec, known := lg.l.Records[k]
			switch {
			case !known:
				rec = Record{Runtime: rt.Kind, Name: m.Name, FirstSeen: now}
				raise(Event{At: now, Kind: New, Runtime: rt.Kind, Name: m.Name, Digest: m.Digest, Format: m.Format, Risky: Risky(m.Format)})
			case m.Digest != "" && rec.Digest != "" && m.Digest != rec.Digest:
				raise(Event{At: now, Kind: Changed, Runtime: rt.Kind, Name: m.Name, Digest: m.Digest, PrevDigest: rec.Digest, Format: m.Format, Risky: Risky(m.Format)})
				rec.FirstSeen = now // a different model under the same name
			}
			rec.Digest, rec.SizeBytes, rec.Format, rec.LastSeen = m.Digest, m.SizeBytes, m.Format, now
			lg.l.Records[k] = rec
		}
	}
	for k, rec := range lg.l.Records {
		if seen[k] || !answered[rec.Runtime] {
			continue
		}
		raise(Event{At: now, Kind: Removed, Runtime: rec.Runtime, Name: rec.Name, Digest: rec.Digest, Format: rec.Format})
		delete(lg.l.Records, k)
	}

	changed := len(events) > 0 || !lg.l.Baselined
	lg.l.Baselined = true
	lg.l.Events = append(lg.l.Events, events...)
	if over := len(lg.l.Events) - maxEvents; over > 0 {
		lg.l.Events = lg.l.Events[over:]
	}
	if !changed {
		return nil, nil
	}
	return events, lg.save()
}

// FirstSeen is when this machine first saw the model now under that name.
func (lg *Ledger) FirstSeen(kind airuntime.Kind, name string) (time.Time, bool) {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	rec, ok := lg.l.Records[key(kind, name)]
	return rec.FirstSeen, ok
}

// Recent returns events since the given time, newest first.
func (lg *Ledger) Recent(since time.Time) []Event {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	var out []Event
	for _, e := range lg.l.Events {
		if e.At.After(since) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

func (lg *Ledger) save() error {
	raw, err := json.MarshalIndent(lg.l, "", "  ")
	if err != nil {
		return err
	}
	tmp := lg.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, lg.path)
}

// Risky reports a format that can run code when loaded: PyTorch's pickle
// files. GGUF, safetensors, MLX and ONNX hold data only.
func Risky(format string) bool {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "pickle", "pt", "pth", "bin", "ckpt", "pkl", "pytorch":
		return true
	}
	return false
}

// Records returns every model in the ledger.
func (lg *Ledger) Records() []Record {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	out := make([]Record, 0, len(lg.l.Records))
	for _, r := range lg.l.Records {
		out = append(out, r)
	}
	return out
}
