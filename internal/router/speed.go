package router

import "sync"

// SpeedBook remembers generation speed per machine and model, as observed by
// this agent's router. It lives in memory: a restarted agent re-measures, and
// tries each machine once to do so.
type SpeedBook struct {
	mu    sync.Mutex
	rates map[string]float64
}

// smoothing weights the newest sample; speeds vary with prompt length and
// whatever else the machine is doing, so one sample shouldn't swing routing.
const smoothing = 0.3

func NewSpeedBook() *SpeedBook { return &SpeedBook{rates: map[string]float64{}} }

func key(machineID, model string) string { return machineID + "\x00" + model }

// Record adds one observation of tokens per second.
func (b *SpeedBook) Record(machineID, model string, tps float64) {
	if tps <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(machineID, model)
	if old, ok := b.rates[k]; ok {
		tps = old + smoothing*(tps-old)
	}
	b.rates[k] = tps
}

func (b *SpeedBook) TokensPerSec(machineID, model string) (float64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tps, ok := b.rates[key(machineID, model)]
	return tps, ok
}
