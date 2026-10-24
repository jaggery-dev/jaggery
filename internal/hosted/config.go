// Package hosted sends model requests to providers outside the house and logs
// every send. It does not inspect content: a privacy gate was tried and
// dropped (see tech-specs/decisions.md, "Agents and content").
//
// Providers are off until the owner configures one in providers.json in the
// state directory. Each must have a spend cap and a model allow-list; its key
// lives in a file only the agent's user can read. The machine that holds a
// provider's key is the house's exit for it.
package hosted

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ConfigFile is the providers' file in the state directory.
const ConfigFile = "providers.json"

// DefaultMaxOutput caps a reply's tokens when the provider entry doesn't.
const DefaultMaxOutput = 1024

// Price is what a provider charges, in US dollars per million tokens.
type Price struct {
	InputPerMTok  float64 `json:"input_per_mtok"`
	OutputPerMTok float64 `json:"output_per_mtok"`
}

// Policy is what the provider says it does with the data, as checked by
// the owner (slice X2 turns this into a shipped, approved catalog).
type Policy struct {
	Retention    string `json:"retention,omitempty"`
	Training     string `json:"training,omitempty"`
	ZDR          string `json:"zdr,omitempty"`
	Jurisdiction string `json:"jurisdiction,omitempty"`
	Attested     bool   `json:"attested,omitempty"`
	Source       string `json:"source,omitempty"`
	Checked      string `json:"checked,omitempty"`
}

// Provider is one hosted model service with an OpenAI-compatible API.
type Provider struct {
	// Name prefixes its models in /v1/models: "anthropic/claude-haiku-4-5".
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	// KeyFile holds the API key, relative to the state directory; it must
	// be readable by its owner only.
	KeyFile string `json:"key_file"`
	// Models are the only models the house may use here.
	Models []string `json:"models"`
	Price  Price    `json:"price"`
	// CapUSD is the most the house may spend here, ever; a request that
	// could take the total past it is refused.
	CapUSD          float64 `json:"cap_usd"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	Policy          Policy  `json:"policy"`
	// Free is the provider's free allowance, used before anything is paid
	// for (slice X5). Tokens count prompt and reply together.
	Free *Free `json:"free,omitempty"`
}

// Free is a free allowance: Tokens per period, where Per is "once" (a
// sign-up allowance), "day" or "month" (calendar, local time).
type Free struct {
	Tokens int64  `json:"tokens"`
	Per    string `json:"per"`
}

// Config is the providers this machine can send to, and named groups of
// their models.
type Config struct {
	Providers []Provider `json:"providers"`
	// Groups name ordered lists of "<provider>/<model>", offered to apps as
	// one model: "coding": ["example/big-model", "other/fast-model"].
	Groups map[string][]string `json:"groups,omitempty"`
}

// Member is one model of a group.
type Member struct {
	Provider *Provider
	Model    string
}

func (m Member) ID() string { return m.Provider.Name + "/" + m.Model }

// Load reads providers.json from stateDir. No file means no providers.
func Load(stateDir string) (Config, error) {
	raw, err := os.ReadFile(filepath.Join(stateDir, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	seen := map[string]bool{}
	for i := range c.Providers {
		p := &c.Providers[i]
		if err := p.validate(); err != nil {
			return Config{}, fmt.Errorf("%s: provider %q: %w", ConfigFile, p.Name, err)
		}
		if seen[p.Name] {
			return Config{}, fmt.Errorf("%s: provider %q is listed twice", ConfigFile, p.Name)
		}
		seen[p.Name] = true
		if p.MaxOutputTokens <= 0 {
			p.MaxOutputTokens = DefaultMaxOutput
		}
	}
	for name, models := range c.Groups {
		if name == "" || strings.ContainsAny(name, "/ ") || seen[name] {
			return Config{}, fmt.Errorf("%s: group %q needs a name without spaces or slashes that isn't a provider's", ConfigFile, name)
		}
		if len(models) == 0 {
			return Config{}, fmt.Errorf("%s: group %q has no models", ConfigFile, name)
		}
		for _, m := range models {
			if _, _, ok := c.Find(m); !ok {
				return Config{}, fmt.Errorf("%s: group %q: %q is not an allowed model of a provider", ConfigFile, name, m)
			}
		}
	}
	return c, nil
}

// Group returns a group's members in order.
func (c Config) Group(name string) ([]Member, bool) {
	models, ok := c.Groups[name]
	if !ok {
		return nil, false
	}
	out := make([]Member, 0, len(models))
	for _, m := range models {
		if p, bare, ok := c.Find(m); ok {
			out = append(out, Member{Provider: p, Model: bare})
		}
	}
	return out, true
}

func (p *Provider) validate() error {
	if p.Name == "" || strings.ContainsAny(p.Name, "/ ") {
		return errors.New("needs a name without spaces or slashes")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("base_url %q is not a URL", p.BaseURL)
	}
	// A provider is reached over TLS. Plain HTTP is only for a vendor's
	// proxy on this machine (an attesting proxy, for example), which does its own TLS.
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return fmt.Errorf("base_url must be https (or http to a proxy on this machine), not %q", p.BaseURL)
	}
	if p.KeyFile == "" {
		return errors.New("needs key_file")
	}
	if len(p.Models) == 0 {
		return errors.New("needs at least one model in models")
	}
	if p.CapUSD <= 0 {
		return errors.New("needs a spend cap (cap_usd above 0)")
	}
	if p.Free != nil {
		if p.Free.Tokens <= 0 {
			return errors.New("free.tokens must be above 0")
		}
		switch p.Free.Per {
		case "once", "day", "month":
		default:
			return fmt.Errorf(`free.per must be "once", "day" or "month", not %q`, p.Free.Per)
		}
	}
	if p.Price.InputPerMTok <= 0 || p.Price.OutputPerMTok <= 0 {
		return errors.New("needs its prices (price.input_per_mtok, price.output_per_mtok), to keep to the cap")
	}
	return nil
}

// Find returns the provider and its own model name for a model the house
// asked for as "<provider>/<model>", if it is on that provider's list.
func (c Config) Find(model string) (*Provider, string, bool) {
	name, bare, ok := strings.Cut(model, "/")
	if !ok {
		return nil, "", false
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.Name != name {
			continue
		}
		for _, m := range p.Models {
			if m == bare {
				return p, bare, true
			}
		}
	}
	return nil, "", false
}

// Named reports whether model asks for a configured provider, even one of
// its models that isn't allowed.
func (c Config) Named(model string) (*Provider, bool) {
	name, _, ok := strings.Cut(model, "/")
	if !ok {
		return nil, false
	}
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], true
		}
	}
	return nil, false
}

// Key reads the provider's API key, refusing a file others can read.
func (p *Provider) Key(stateDir string) (string, error) {
	path := p.KeyFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(stateDir, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s's key: %w", p.Name, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s's key file %s can be read by others: chmod 600 it", p.Name, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", fmt.Errorf("%s's key file %s is empty", p.Name, path)
	}
	return key, nil
}

// Cost is what prompt and completion tokens cost here, in dollars.
func (p *Provider) Cost(prompt, completion int) float64 {
	return float64(prompt)*p.Price.InputPerMTok/1e6 + float64(completion)*p.Price.OutputPerMTok/1e6
}
