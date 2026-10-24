package main

import (
	"errors"
	"flag"
	"io"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/home"
)

func TestExitCodes(t *testing.T) {
	badFlag := func() error {
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return parseFlags(fs, []string{"--bogus"})
	}
	pass1 := []checkResult{{"agent", pass, "", ""}}
	warn1 := append(pass1, checkResult{"clock", warn, "", ""})
	fail1 := append(warn1, checkResult{"network", fail, "", ""})
	for _, c := range []struct {
		name string
		err  error
		code int
		show bool
	}{
		{"ok", nil, 0, false},
		{"failed", errors.New("the agent is not running"), 1, true},
		{"usage", usagef("usage: jag approve <id>"), 2, true},
		{"bad flag", badFlag(), 2, true},
		{"doctor passed", doctorResult(pass1), 0, false},
		{"doctor warned", doctorResult(warn1), 4, false},
		{"doctor failed", doctorResult(fail1), 5, true},
	} {
		code, show := exitCode(c.err)
		if code != c.code || show != c.show {
			t.Errorf("%s: exit %d (print %v), want %d (print %v)", c.name, code, show, c.code, c.show)
		}
	}
	if exitCrash != 3 {
		t.Errorf("test crash builds exit %d, want 3", exitCrash)
	}
	// The table in the reference lists every code once, in order.
	for i, c := range exitCodes {
		if c.code != i {
			t.Errorf("exitCodes[%d] is code %d", i, c.code)
		}
	}
	if fs := flag.NewFlagSet("x", flag.ContinueOnError); !errors.Is(parseFlags(fs, []string{"-h"}), flag.ErrHelp) {
		t.Error("asking for help became a usage error")
	}
}

// Wrong usage from a subcommand exits 2, not 1.
func TestSubcommandUsageExits2(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"remove"}} {
		if code, _ := exitCode(share(args)); code != exitUsage {
			t.Errorf("jag share %v: exit %d", args, code)
		}
	}
	if code, _ := exitCode(setTrust(nil, home.Approved)); code != exitUsage {
		t.Errorf("jag approve with no id: exit %d", code)
	}
}
