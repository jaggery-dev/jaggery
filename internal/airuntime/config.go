package airuntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
)

// ConfigFile lists runtimes on ports other than their defaults, in the state
// directory: {"runtimes": [{"kind": "llamacpp", "port": 8181}]}. The owner
// edits it; it is read again when it changes, so no restart is needed. The
// default ports are still probed, and each entry must still answer in its
// runtime's own shape, so a wrong port finds nothing rather than a web app.
const ConfigFile = "runtimes.json"

type config struct {
	Runtimes []Port `json:"runtimes"`
}

// LoadConfig reads runtimes.json from stateDir. No file means no entries.
func LoadConfig(stateDir string) ([]Port, error) {
	raw, err := os.ReadFile(filepath.Join(stateDir, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	seen := map[int]bool{}
	for _, p := range c.Runtimes {
		if _, ok := detectors[p.Kind]; !ok {
			return nil, fmt.Errorf("%s: unknown kind %q: use one of ollama, lmstudio, llamacpp, vllm", ConfigFile, p.Kind)
		}
		if p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("%s: %s: port %d is not a port (1 to 65535)", ConfigFile, p.Kind, p.Port)
		}
		if seen[p.Port] {
			return nil, fmt.Errorf("%s: port %d is listed twice", ConfigFile, p.Port)
		}
		seen[p.Port] = true
	}
	return c.Runtimes, nil
}

// reloadEvery is how often Ports looks at runtimes.json again.
var reloadEvery = 2 * time.Second

var cfg struct {
	mu      sync.Mutex
	checked time.Time
	path    string
	modTime time.Time
	size    int64
	ports   []Port
	err     error
}

// Ports is where Detect looks: the owner's entries first, so an entry on a
// default port overrides that default's kind, then the defaults. A
// runtimes.json that can't be read is reported once and ignored.
func Ports() []Port {
	out := append([]Port(nil), configured()...)
	for _, k := range Kinds {
		out = append(out, Port{k, DefaultPorts[k]})
	}
	return out
}

// ConfigError is why runtimes.json was ignored, if it was.
func ConfigError() error {
	configured()
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	return cfg.err
}

func configured() []Port {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	now := time.Now()
	if !cfg.checked.IsZero() && now.Sub(cfg.checked) < reloadEvery {
		return cfg.ports
	}
	cfg.checked = now
	dir, err := identity.StateDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(dir, ConfigFile)
	var mod time.Time
	size := int64(-1)
	if info, err := os.Stat(path); err == nil {
		mod, size = info.ModTime(), info.Size()
	}
	if path == cfg.path && mod.Equal(cfg.modTime) && size == cfg.size {
		return cfg.ports
	}
	cfg.path, cfg.modTime, cfg.size = path, mod, size
	ports, err := LoadConfig(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring", err)
		ports = nil
	}
	cfg.ports, cfg.err = ports, err
	return ports
}
