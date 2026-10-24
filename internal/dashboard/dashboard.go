// Package dashboard is the embedded web UI an agent serves at jaggery.local.
// It is a single static page that polls the agent for the house snapshot and
// draws a card per machine; keeping it dependency-free (no build step) is what
// lets the agent stay one static binary.
package dashboard

import _ "embed"

//go:embed index.html
var indexHTML []byte

// Index returns the dashboard page.
func Index() []byte { return indexHTML }
