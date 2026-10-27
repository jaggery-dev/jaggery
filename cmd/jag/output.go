package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jaggery-dev/jaggery/internal/compat"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/shares"
)

// jsonOutput is set by --json (G1): the command prints one envelope on
// stdout instead of text for people. Warnings and progress still go to
// stderr.
var jsonOutput bool

// jsonCommands take --json; "files" and "update" check their subcommand
// and flags themselves.
var jsonCommands = map[string]bool{
	"status": true, "peers": true, "map": true, "models": true, "doctor": true,
	"activity": true, "outbound": true, "share": true, "shares": true,
	"files": true, "update": true, "ai": true,
}

// takeJSONFlag removes --json from a command's arguments and says whether
// it was there. Arguments after "--" are left alone.
func takeJSONFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--json" || a == "-json" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

// envelope is what every --json command prints: data when it worked, error
// when it didn't. doctor sets both when a check failed.
type envelope struct {
	Version int        `json:"version"`
	OK      bool       `json:"ok"`
	Data    any        `json:"data,omitempty"`
	Error   *jsonError `json:"error,omitempty"`
}

// jsonError is a failure an agent can branch on: Code is stable, Message is
// for people and may change, Fix is the command that fixes it, when there
// is one.
type jsonError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// codedError is an error with a stable code and a fix, for --json.
type codedError struct {
	code, fix string
	err       error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

// errNotRunning is the agent not answering on this machine.
func errNotRunning(err error) error {
	return codedError{"not_running", "jag up, or sudo systemctl start jaggery",
		fmt.Errorf("the agent isn't running here: start `jag up` (%w)", err)}
}

// errJSONNotHere is --json on a command or form that doesn't take it.
func errJSONNotHere(cmd string) error {
	return usagef("%s doesn't take --json: only commands that show something do (docs/reference/json.md)", cmd)
}

// toJSONError gives err its stable code.
func toJSONError(err error) *jsonError {
	var ce codedError
	var ue usageError
	var ee exitError
	switch {
	case errors.As(err, &ce):
		return &jsonError{ce.code, err.Error(), ce.fix}
	case errors.As(err, &ue):
		return &jsonError{"usage", err.Error(), ""}
	case errors.Is(err, identity.ErrNotInitialized):
		return &jsonError{"no_identity", err.Error(), "jag up"}
	case errors.Is(err, house.ErrNoSnapshot):
		return &jsonError{"no_snapshot", err.Error(), "jag up, or sudo systemctl start jaggery"}
	case errors.Is(err, shares.ErrNotFound):
		return &jsonError{"not_found", err.Error(), "jag share"}
	case errors.As(err, &ee) && ee.code == exitDoctorFail:
		return &jsonError{"checks_failed", err.Error(), ""}
	}
	return &jsonError{"failed", err.Error(), ""}
}

// writeEnvelope prints e on w, one JSON document.
func writeEnvelope(w io.Writer, e envelope) error {
	e.Version = compat.CLIOutput
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(e)
}

// emitJSON prints a command's result.
func emitJSON(data any) error {
	return writeEnvelope(os.Stdout, envelope{OK: true, Data: data})
}

// emitJSONError prints a failed command's envelope; main calls it for any
// error a --json command returns. data, when set, is a partial result.
func emitJSONError(err error, data any) {
	_ = writeEnvelope(os.Stdout, envelope{Data: data, Error: toJSONError(err)})
}
