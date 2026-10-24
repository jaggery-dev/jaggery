package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/shares"
)

const shareHelp = `usage: jag share                                       folders lent to models and agents, house-wide
       jag share add <folder> [--name <name>] [--writable]  lend a folder on this machine
       jag share remove <name>                         stop lending it (the folder is untouched)

A share is read-only unless added with --writable. Shares are added and removed
only on the machine that holds the folder; the rest of the house sees the name,
never the path. Dotfiles and dot-folders (.git, .ssh, .env) and *.key and *.pem
files are never served. The agent can serve only what its user can read.

For a NAS, mount it on a machine and share the mount point, or run jag on the
NAS itself.

Models and agents reach every share in the house through the MCP server at
http://ai.local/mcp (streamable HTTP), on any machine.
`

func share(args []string) error {
	if len(args) == 0 {
		return listShares()
	}
	if jsonOutput && args[0] != "-h" && args[0] != "--help" && args[0] != "help" {
		return errJSONNotHere("jag share " + args[0])
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("share add", flag.ContinueOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, shareHelp) }
		name := fs.String("name", "", "the name models and agents use")
		writable := fs.Bool("writable", false, "let models and agents change files (old versions are kept)")
		folder, rest := splitFolder(args[1:])
		if err := parseFlags(fs, rest); err != nil {
			return err
		}
		if folder == "" && fs.NArg() == 1 {
			folder = fs.Arg(0)
		} else if folder == "" || fs.NArg() != 0 {
			return usagef("usage: jag share add <folder> [--name <name>] [--writable]")
		}
		s, err := shares.Add(dir, folder, *name, *writable, time.Now())
		if err != nil {
			return err
		}
		mode := "read-only"
		if s.Writable {
			mode = "writable"
		}
		fmt.Printf("sharing %s as %q (%s)\n", s.Path, s.Name, mode)
		fmt.Println("the rest of the house sees it within a scan")
		return nil
	case "remove", "rm":
		if len(args) != 2 {
			return usagef("usage: jag share remove <name>")
		}
		if err := shares.Remove(dir, args[1]); err != nil {
			return err
		}
		fmt.Printf("stopped sharing %q; the folder is untouched\n", args[1])
		return nil
	case "list", "ls":
		return listShares()
	case "-h", "--help", "help":
		fmt.Print(shareHelp)
		return nil
	}
	return usagef("unknown share command %q\n\n%s", args[0], shareHelp)
}

// splitFolder lets the folder come before or after the flags: Go's flag
// package stops at the first non-flag argument.
func splitFolder(args []string) (string, []string) {
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		return args[0], args[1:]
	}
	return "", args
}

func listShares() error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	local, err := shares.Load(dir)
	if err != nil {
		return err
	}
	if jsonOutput {
		return sharesJSON(local, loadHouse())
	}
	fmt.Println("on this machine:")
	if len(local) == 0 {
		fmt.Println("  nothing shared (jag share add <folder>)")
	}
	for _, s := range local {
		fmt.Printf("  %-16s %-10s %s\n", s.Name, shareMode(s.Writable), s.Path)
	}

	snap := loadHouse()
	var others int
	for _, m := range snap.Peers {
		for _, s := range m.Inventory.Shares {
			if others == 0 {
				fmt.Println("\nelsewhere in the house:")
			}
			others++
			state := ""
			if !m.Reachable {
				state = "  (offline)"
			}
			fmt.Printf("  %-16s %-10s on %s%s\n", s.Name, shareMode(s.Writable), m.Name, state)
		}
	}
	return nil
}

func shareMode(writable bool) string {
	if writable {
		return "writable"
	}
	return "read-only"
}

// shareJSON is one share in `jag share --json`. Path is set only for this
// machine's shares: the rest of the house never sees it.
type shareJSON struct {
	Name     string `json:"name"`
	Machine  string `json:"machine"`
	Self     bool   `json:"self,omitempty"`
	Writable bool   `json:"writable"`
	Path     string `json:"path,omitempty"`
	Online   bool   `json:"online"`
}

func sharesJSON(local []shares.Share, snap house.Snapshot) error {
	out := []shareJSON{}
	for _, s := range local {
		out = append(out, shareJSON{Name: s.Name, Machine: snap.Self.Name, Self: true, Writable: s.Writable, Path: s.Path, Online: true})
	}
	for _, m := range snap.Peers {
		for _, s := range m.Inventory.Shares {
			out = append(out, shareJSON{Name: s.Name, Machine: m.Name, Writable: s.Writable, Online: m.Reachable})
		}
	}
	return emitJSON(map[string]any{"shares": out})
}
