package hosted

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The outbound log (slice O2) is the house's record of what left it and to
// whom. It has two halves, both kept on the machine that sent, readable by
// the agent's user only:
//   - one line per request to a provider, sent or refused: who, which
//     provider and model, the gate's verdict and what it found (types and
//     counts, never values), tokens and cost;
//   - for each request that was sent, the exact body sent and the reply,
//     so the owner can see precisely what the provider saw. A refused
//     request's body is never written: it was refused for holding private
//     data.
const (
	outboundDir     = "outbound"
	payloadDir      = "payloads"
	OutboundKeep    = 30 * 24 * time.Hour
	outboundDayFile = "2006-01-02"
	// MaxPayloadReply bounds how much of one reply is kept.
	MaxPayloadReply = 1 << 20
)

// Entry is one request to a hosted provider.
type Entry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Device   string    `json:"device,omitempty"`
	App      string    `json:"app,omitempty"`
	User     string    `json:"user,omitempty"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	// Group is the model group the request asked for, if any (X5).
	Group string `json:"group,omitempty"`
	// Confirmed says the person replied "send" to a warning about what the
	// gate found (O3).
	Confirmed bool `json:"confirmed,omitempty"`
	// Verdict is the gate's tier; Entities what it found, by type.
	Verdict  string         `json:"verdict"`
	Entities map[string]int `json:"entities,omitempty"`
	// Sent says whether anything left the house; Reason why not.
	Sent             bool    `json:"sent"`
	Reason           string  `json:"reason,omitempty"`
	Status           int     `json:"status"`
	BytesSent        int     `json:"bytes_sent,omitempty"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	DurationMS       int64   `json:"duration_ms"`
}

// Payload is what was sent and what came back for one entry, byte for byte.
type Payload struct {
	ID    string `json:"id"`
	Sent  string `json:"sent"`
	Reply string `json:"reply"`
	// Truncated says the reply was longer than MaxPayloadReply.
	Truncated bool `json:"truncated,omitempty"`
}

// Log is the outbound log in a state directory.
type Log struct {
	dir string
	mu  sync.Mutex
}

// OpenLog creates the log's directories, readable by their owner only.
func OpenLog(stateDir string) (*Log, error) {
	dir := filepath.Join(stateDir, outboundDir)
	if err := os.MkdirAll(filepath.Join(dir, payloadDir), 0o700); err != nil {
		return nil, err
	}
	for _, d := range []string{dir, filepath.Join(dir, payloadDir)} {
		if err := os.Chmod(d, 0o700); err != nil {
			return nil, err
		}
	}
	return &Log{dir: dir}, nil
}

// NewID names an entry: time-ordered and unguessable.
func NewID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

func (l *Log) dayPath(day time.Time) string {
	return filepath.Join(l.dir, day.Local().Format(outboundDayFile)+".jsonl")
}

func (l *Log) payloadPath(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return "", fmt.Errorf("bad outbound id %q", id)
	}
	return filepath.Join(l.dir, payloadDir, id+".json"), nil
}

// Append records e in its day's file.
func (l *Log) Append(e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.dayPath(e.At), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// SavePayload keeps the body sent and the reply for entry id.
func (l *Log) SavePayload(id string, sent, reply []byte) error {
	path, err := l.payloadPath(id)
	if err != nil {
		return err
	}
	p := Payload{ID: id, Sent: string(sent)}
	if len(reply) > MaxPayloadReply {
		reply, p.Truncated = reply[:MaxPayloadReply], true
	}
	p.Reply = string(reply)
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// Day returns the entries of day (local time), oldest first.
func (l *Log) Day(day time.Time) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.dayPath(day))
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

// ReadPayload returns what entry id sent and received.
func (l *Log) ReadPayload(id string) (Payload, error) {
	path, err := l.payloadPath(id)
	if err != nil {
		return Payload{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Payload{}, err
	}
	var p Payload
	return p, json.Unmarshal(raw, &p)
}

// Prune deletes days and payloads older than OutboundKeep.
func (l *Log) Prune(now time.Time) {
	cutoff := now.Add(-OutboundKeep)
	if entries, err := os.ReadDir(l.dir); err == nil {
		for _, e := range entries {
			day, err := time.ParseInLocation(outboundDayFile, strings.TrimSuffix(e.Name(), ".jsonl"), time.Local)
			if err == nil && day.Before(cutoff) {
				_ = os.Remove(filepath.Join(l.dir, e.Name()))
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(l.dir, payloadDir)); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(l.dir, payloadDir, e.Name()))
			}
		}
	}
}
