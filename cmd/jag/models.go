package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/modelwatch"
)

const modelCheckInterval = 30 * time.Second

// modelLedger is the running agent's; nil in the CLI.
var modelLedger *modelwatch.Ledger

// watchModels records the runtimes' models in the ledger and says when one
// appears, changes or goes.
func watchModels(ctx context.Context, lg *modelwatch.Ledger) {
	ticker := time.NewTicker(modelCheckInterval)
	defer ticker.Stop()
	for {
		events, err := lg.Observe(airuntime.Detect(ctx), time.Now().UTC())
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save the model ledger:", err)
		}
		for _, e := range events {
			fmt.Println(describeModelEvent(e))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func describeModelEvent(e modelwatch.Event) string {
	var msg string
	switch e.Kind {
	case modelwatch.New:
		msg = fmt.Sprintf("model alert: new model %s in %s (%s, digest %s)", e.Name, e.Runtime, orUnknown(e.Format), shortDigest(e.Digest))
	case modelwatch.Changed:
		msg = fmt.Sprintf("model alert: %s in %s changed (digest %s, was %s)", e.Name, e.Runtime, shortDigest(e.Digest), shortDigest(e.PrevDigest))
	default:
		msg = fmt.Sprintf("model %s removed from %s", e.Name, e.Runtime)
	}
	if e.Risky {
		msg += "; WARNING: this format can run code when loaded"
	}
	return msg
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return orUnknown(d)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// addModelFacts puts the ledger's first-seen times and recent events into a
// report the agent hands out.
func addModelFacts(r *inventory.Report, now time.Time) {
	if modelLedger == nil {
		return
	}
	for i := range r.Runtimes {
		rt := &r.Runtimes[i]
		for j := range rt.Models {
			if t, ok := modelLedger.FirstSeen(rt.Kind, rt.Models[j].Name); ok {
				rt.Models[j].FirstSeen = &t
			}
		}
	}
	r.ModelEvents = modelLedger.Recent(now.Add(-modelwatch.EventWindow))
	markPending(r.Runtimes)
	loadingNow.mark(r.Runtimes)
	r.ToolEvents = toolSeen.recent(now)
}
