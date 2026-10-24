package shares

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The access log: one line per call on a share, on the machine
// that served it, and on the machine that took the call when that is a
// different one. A file per day in the state directory, readable by the
// agent's user only, kept as long as old versions are.

const (
	logDir    = "files"
	logLayout = "2006-01-02"
)

// LogEntry is one call on a share.
type LogEntry struct {
	Time time.Time `json:"time"`
	// App is the MCP client's name from its handshake; From the address the
	// call came from.
	App  string `json:"app,omitempty"`
	From string `json:"from,omitempty"`
	// Via is the machine that took the call and sent it here; ServedBy the
	// machine this one sent it to. Both empty when one machine did both.
	Via      string `json:"via,omitempty"`
	ServedBy string `json:"served_by,omitempty"`
	Op       string `json:"op"`
	Share    string `json:"share"`
	Path     string `json:"path,omitempty"`
	Query    string `json:"query,omitempty"`
	// Bytes read or written; Matches found by a search.
	Bytes   int64 `json:"bytes,omitempty"`
	Matches int   `json:"matches,omitempty"`
	// OldSHA256 and NewSHA256 are the contents before and after a write,
	// delete or restore; Version is where the old contents went.
	OldSHA256 string `json:"old_sha256,omitempty"`
	NewSHA256 string `json:"new_sha256,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Use is a share's calls today, for the dashboard: counts and app names,
// never paths.
type Use struct {
	Reads    int      `json:"reads,omitempty"`
	Writes   int      `json:"writes,omitempty"`
	Deletes  int      `json:"deletes,omitempty"`
	Searches int      `json:"searches,omitempty"`
	Lists    int      `json:"lists,omitempty"`
	Refused  int      `json:"refused,omitempty"`
	Apps     []string `json:"apps,omitempty"`
}

var logMu sync.Mutex

func logPath(dir string, day time.Time) string {
	return filepath.Join(dir, logDir, day.Local().Format(logLayout)+".jsonl")
}

// AppendLog records e in its day's file. The first entry of a day also drops
// days older than KeepVersions.
func AppendLog(dir string, e LogEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	logMu.Lock()
	defer logMu.Unlock()
	if err := os.MkdirAll(filepath.Join(dir, logDir), 0o700); err != nil {
		return err
	}
	p := logPath(dir, e.Time)
	_, statErr := os.Stat(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if errors.Is(statErr, os.ErrNotExist) {
		pruneLog(dir, e.Time)
	}
	return err
}

func pruneLog(dir string, now time.Time) {
	entries, err := os.ReadDir(filepath.Join(dir, logDir))
	if err != nil {
		return
	}
	cutoff := now.Add(-KeepVersions)
	for _, e := range entries {
		day, err := time.ParseInLocation(logLayout, strings.TrimSuffix(e.Name(), ".jsonl"), time.Local)
		if err == nil && day.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, logDir, e.Name()))
		}
	}
}

// ReadLog returns a day's entries (local time), oldest first.
func ReadLog(dir string, day time.Time) ([]LogEntry, error) {
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.Open(logPath(dir, day))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e LogEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// UseToday counts today's calls on the shares this machine holds.
func UseToday(dir string, now time.Time) map[string]*Use {
	entries, _ := ReadLog(dir, now)
	out := map[string]*Use{}
	apps := map[string]map[string]bool{}
	for _, e := range entries {
		if e.ServedBy != "" {
			continue // served by another machine, which counts it
		}
		u := out[e.Share]
		if u == nil {
			u = &Use{}
			out[e.Share] = u
			apps[e.Share] = map[string]bool{}
		}
		if e.App != "" {
			apps[e.Share][e.App] = true
		}
		if e.Error != "" {
			u.Refused++
			continue
		}
		switch e.Op {
		case OpRead:
			u.Reads++
		case OpWrite, OpRestore:
			u.Writes++
		case OpDelete:
			u.Deletes++
		case OpSearch:
			u.Searches++
		case OpList:
			u.Lists++
		}
	}
	for share, set := range apps {
		for a := range set {
			out[share].Apps = append(out[share].Apps, a)
		}
		sort.Strings(out[share].Apps)
	}
	return out
}
