package hosted

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const spendFile = "hosted-spend.json"

// Usage is what the house has used at one provider, ever.
type Usage struct {
	USD              float64 `json:"usd"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	Requests         int     `json:"requests"`
	// Estimated counts requests whose tokens the provider didn't report.
	Estimated int `json:"estimated,omitempty"`
	// Periods counts tokens (prompt and reply) per free-allowance period:
	// "once", "day:2026-10-10", "month:2026-10". Only the current day and
	// month are kept.
	Periods map[string]int64 `json:"periods,omitempty"`
}

// periodKey is the ledger key for a free allowance's current period.
func periodKey(per string, now time.Time) string {
	switch per {
	case "day":
		return "day:" + now.Local().Format("2006-01-02")
	case "month":
		return "month:" + now.Local().Format("2006-01")
	}
	return "once"
}

// Ledger keeps each provider's running total in the state directory, so a
// restart can't reset a spend cap.
type Ledger struct {
	path string
	mu   sync.Mutex
	used map[string]Usage
	// held is the worst case of requests in flight, so two at once can't
	// both fit under a cap that only has room for one.
	held map[string]float64
}

// OpenLedger reads the totals; a missing file starts at zero, a damaged one
// is an error (starting at zero would lift the caps).
func OpenLedger(stateDir string) (*Ledger, error) {
	l := &Ledger{path: filepath.Join(stateDir, spendFile), used: map[string]Usage{}, held: map[string]float64{}}
	raw, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &l.used); err != nil {
		return nil, err
	}
	return l, nil
}

// Used is a provider's total so far.
func (l *Ledger) Used(provider string) Usage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used[provider]
}

// FreeLeft is how many tokens of p's free allowance are left in the
// current period: 0 when it has none.
func (l *Ledger) FreeLeft(p *Provider, now time.Time) int64 {
	if p.Free == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	left := p.Free.Tokens - l.used[p.Name].Periods[periodKey(p.Free.Per, now)]
	return max(0, left)
}

// Bill is what prompt and completion tokens cost at p right now: the part
// beyond what's left of the free allowance, at the provider's prices. The
// provider's own meter is the authority; a quota error from it (a 429 or
// 402) wins over this count.
func (l *Ledger) Bill(p *Provider, prompt, completion int, now time.Time) float64 {
	total := prompt + completion
	if total == 0 {
		return 0
	}
	free := l.FreeLeft(p, now)
	if free >= int64(total) {
		return 0
	}
	return p.Cost(prompt, completion) * float64(int64(total)-free) / float64(total)
}

// Reserve holds worst dollars against the provider's cap, if they fit
// beside what's spent and what other requests hold. Every reservation that
// succeeds must be settled.
func (l *Ledger) Reserve(p *Provider, worst float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used[p.Name].USD+l.held[p.Name]+worst > p.CapUSD {
		return false
	}
	l.held[p.Name] += worst
	return true
}

// Settle releases a reservation of worst dollars and records what the
// request actually used (nothing, if it never reached the provider), then
// saves the totals.
func (l *Ledger) Settle(provider string, worst float64, prompt, completion int, usd float64, estimated bool) error {
	return l.SettleAt(provider, worst, prompt, completion, usd, estimated, nil, time.Now())
}

// SettleAt is Settle that also counts the tokens against free, the
// provider's free allowance (nil for none), at now.
func (l *Ledger) SettleAt(provider string, worst float64, prompt, completion int, usd float64, estimated bool, free *Free, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held[provider] -= worst
	if prompt == 0 && completion == 0 && usd == 0 {
		return nil
	}
	u := l.used[provider]
	if free != nil {
		key := periodKey(free.Per, now)
		periods := map[string]int64{}
		for k, v := range u.Periods {
			// Keep "once" and the current period; older days and months go.
			if k == "once" || k == key || !strings.HasPrefix(k, free.Per+":") {
				periods[k] = v
			}
		}
		periods[key] += int64(prompt + completion)
		u.Periods = periods
	}
	u.USD += usd
	u.PromptTokens += int64(prompt)
	u.CompletionTokens += int64(completion)
	u.Requests++
	if estimated {
		u.Estimated++
	}
	l.used[provider] = u
	raw, err := json.MarshalIndent(l.used, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}
