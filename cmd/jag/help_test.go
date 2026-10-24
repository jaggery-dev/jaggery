package main

import (
	"strings"
	"testing"
)

// commandsInUsage is every command jag help lists.
func commandsInUsage(t *testing.T) []string {
	t.Helper()
	var cmds []string
	seen := map[string]bool{}
	for _, line := range strings.Split(usage, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "jag" || !strings.HasPrefix(line, "  jag ") || seen[f[1]] {
			continue
		}
		seen[f[1]] = true
		cmds = append(cmds, f[1])
	}
	if len(cmds) < 15 {
		t.Fatalf("found only %d commands in the usage text: %v", len(cmds), cmds)
	}
	return cmds
}

func TestEveryCommandAnswersHelp(t *testing.T) {
	for _, cmd := range commandsInUsage(t) {
		for _, args := range [][]string{{cmd, "--help"}, {cmd, "-h"}, {"help", cmd}} {
			text, ok := helpRequested(args)
			if !ok {
				t.Errorf("jag %s prints no usage", strings.Join(args, " "))
				continue
			}
			if !strings.HasPrefix(text, "usage: jag "+cmd) {
				t.Errorf("jag %s: usage starts %q, want it to name the command", strings.Join(args, " "), strings.SplitN(text, "\n", 2)[0])
			}
		}
	}
}

func TestHelpIsOnlyTakenFromTheFirstArgument(t *testing.T) {
	for _, args := range [][]string{
		{"join"},
		{"join", "maple-river-otter-candle-quartz-fig"},
		{"approve", "1a2b", "--help"}, // goes to the command, which says its own usage
		{"help"},
		{"nonsense", "--help"},
		{"help", "nonsense"},
	} {
		if _, ok := helpRequested(args); ok {
			t.Errorf("jag %s was taken as asking for help", strings.Join(args, " "))
		}
	}
}

func TestAliasesShareTheirCommandsHelp(t *testing.T) {
	for alias, cmd := range map[string]string{"app": "apps", "shares": "share", "--version": "version"} {
		a, _ := helpFor(alias)
		c, _ := helpFor(cmd)
		if a == "" || a != c {
			t.Errorf("jag %s --help differs from jag %s --help", alias, cmd)
		}
	}
}
