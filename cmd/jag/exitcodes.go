package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// Exit codes, documented in docs/reference/cli.md (G2): scripts and agents
// can branch on them without reading the output.
const (
	exitOK     = 0 // the command did what was asked
	exitFailed = 1 // it didn't: the error says why
	exitUsage  = 2 // the command line was wrong: an unknown command, flag or argument
	// exitCrash is used only by test builds that crash on purpose
	// (testCrashAfter); releases never exit with it.
	exitCrash      = 3
	exitDoctorWarn = 4 // jag doctor: no check failed, at least one warned
	exitDoctorFail = 5 // jag doctor: at least one check failed
)

// exitCodes is the table in the reference, generated from here.
var exitCodes = []struct {
	code    int
	meaning string
}{
	{exitOK, "done; for jag doctor, every check passed"},
	{exitFailed, "failed: the error on stderr says why and how to fix it"},
	{exitUsage, "wrong usage: an unknown command, flag or argument"},
	{exitCrash, "only test builds that crash on purpose; releases never use it"},
	{exitDoctorWarn, "jag doctor: nothing failed, but at least one check warned"},
	{exitDoctorFail, "jag doctor: at least one check failed"},
}

// usageError is a command line that doesn't fit the command.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usagef(format string, a ...any) error {
	return usageError{fmt.Errorf(format, a...)}
}

// exitError ends the command with a code of its own; Err, when set, is
// printed first.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}
func (e exitError) Unwrap() error { return e.err }

// parseFlags is fs.Parse, with a bad flag counted as wrong usage. The flag
// set has already printed the problem and the command's usage.
func parseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		return usageError{err}
	}
	return err
}

// exitCode is the code main exits with for err, and whether to print err.
func exitCode(err error) (int, bool) {
	var ee exitError
	var ue usageError
	switch {
	case err == nil:
		return exitOK, false
	case errors.As(err, &ee):
		return ee.code, ee.err != nil
	case errors.As(err, &ue):
		return exitUsage, true
	}
	return exitFailed, true
}

// exitCodesHelp is `jag help exit-codes`.
func exitCodesHelp() string {
	var b strings.Builder
	b.WriteString("usage: jag help exit-codes\n\nWhat jag's exit codes mean, for scripts and agents:\n\n")
	for _, c := range exitCodes {
		fmt.Fprintf(&b, "  %d  %s\n", c.code, c.meaning)
	}
	return b.String()
}
