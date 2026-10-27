package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

const aiHelp = `usage: jag ai                    whether this machine takes AI requests
       jag ai pause [--for <time>]  send AI requests elsewhere, for example while a game runs
       jag ai resume            take AI requests again

While a machine is paused, ai.local sends its requests to another machine
that has the model. A request for a model only a paused machine has is
refused with a message saying so, and jag activity notes the machines passed
over. The machine stays on the map and keeps its models. --for takes a time
like 2h or 90m; without it the pause lasts until jag ai resume. Run these on
the machine itself; the rest of the house hears within about 20 seconds.
`

// pauseFile holds this machine's pause; no file means not paused.
const pauseFile = "ai-pause.json"

func aiCmd(args []string) error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return showPause(dir)
	}
	if jsonOutput {
		return errJSONNotHere("jag ai " + args[0])
	}
	switch args[0] {
	case "pause":
		fs := flag.NewFlagSet("ai pause", flag.ContinueOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, aiHelp) }
		var d time.Duration
		fs.DurationVar(&d, "for", 0, "how long, like 2h or 90m (default: until jag ai resume)")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return usagef("jag ai pause takes no names: run it on the machine to pause, for example jag ai pause --for 2h")
		}
		if d < 0 {
			return usagef("--for %s is in the past: give a time like 2h", d)
		}
		now := time.Now()
		p := inventory.Pause{Since: now.UTC()}
		if d > 0 {
			until := now.Add(d).UTC()
			p.Until = &until
		}
		if err := savePause(dir, &p); err != nil {
			return err
		}
		fmt.Printf("AI requests now go to other machines, %s. Undo with jag ai resume.\n", pauseEnd(&p))
		return nil
	case "resume":
		if len(args) > 1 {
			return usagef("jag ai resume takes nothing else")
		}
		was := loadPause(dir, time.Now())
		if err := savePause(dir, nil); err != nil {
			return err
		}
		if was == nil {
			fmt.Println("this machine wasn't paused; it takes AI requests")
			return nil
		}
		fmt.Println("this machine takes AI requests again")
		return nil
	}
	return usagef("unknown jag ai command %q: use pause or resume (jag ai --help)", args[0])
}

func showPause(dir string) error {
	p := loadPause(dir, time.Now())
	if jsonOutput {
		return emitJSON(map[string]any{"paused": p != nil, "pause": p})
	}
	if p == nil {
		fmt.Println("this machine takes AI requests (pause it with jag ai pause)")
		return nil
	}
	fmt.Printf("AI requests are paused here %s (since %s). Undo with jag ai resume.\n", pauseEnd(p), p.Since.Local().Format("15:04"))
	return nil
}

// pauseEnd says when a pause ends, for people: "until 21:30" or "until you
// resume".
func pauseEnd(p *inventory.Pause) string {
	if p.Until == nil {
		return "until you resume"
	}
	until := p.Until.Local()
	if until.YearDay() != time.Now().YearDay() || until.Year() != time.Now().Year() {
		return "until " + until.Format("Mon 2 Jan 15:04")
	}
	return "until " + until.Format("15:04")
}

// loadPause reads this machine's pause; nil when there is none or it has
// run out. A file that can't be read counts as not paused: the owner sees
// requests arrive and pauses again.
func loadPause(dir string, now time.Time) *inventory.Pause {
	raw, err := os.ReadFile(filepath.Join(dir, pauseFile))
	if err != nil {
		return nil
	}
	var p inventory.Pause
	if json.Unmarshal(raw, &p) != nil || !p.Active(now) {
		return nil
	}
	return &p
}

// savePause writes the pause, or removes it when p is nil.
func savePause(dir string, p *inventory.Pause) error {
	path := filepath.Join(dir, pauseFile)
	if p == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// pausedHere is this machine's pause right now, read from the file rather
// than the house snapshot, so a pause takes effect here at once.
func pausedHere() *inventory.Pause {
	dir, err := identity.StateDir()
	if err != nil {
		return nil
	}
	return loadPause(dir, time.Now())
}

// withLivePause gives this machine's entry in machines its current pause.
func withLivePause(machines []house.Machine) []house.Machine {
	for i := range machines {
		if machines[i].Self {
			machines[i].Inventory.AIPaused = pausedHere()
		}
	}
	return machines
}
