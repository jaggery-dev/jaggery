package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jaggery-dev/jaggery/internal/apps"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
)

const appsHelp = `usage: jag apps                                  the apps on the page, house-wide
       jag apps add <name> --port <port> [--path /web] [--description <text>]
                                                 an app on this machine Jaggery doesn't know
       jag apps add <name> --url <address> [--description <text>]
                                                 a link: the router, a printer, a NAS's settings
       jag apps add <name> --description <text>  a new description for an app Jaggery found
       jag apps remove <name>                    take it off the page
       jag apps name <app> <name>                give an app on this machine a name:
                                                 the house opens it at <name>.local
       jag apps name <app> --remove              take the name away

Jaggery finds well-known apps by itself. Add the rest on the machine that runs
them; every phone and laptop in the house sees them on the page within a
minute. An app added by --port is shown only while that port answers on this
machine's network address, so the page never links to something that's off.
Adding a name again replaces it; an entry on a found app's port renames it.

An app's name (jag apps name "Open WebUI" chat) is one word of lower-case
letters, digits and hyphens; the house then opens the app at http://chat.local.
It can't be a machine's name, Jaggery's own (jaggery, ai), or a name another
app in the house already has.
`

func appsCmd(args []string) error {
	if len(args) == 0 {
		return listApps()
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		if err := straightQuotes(args[0], args[1:]); err != nil {
			return err
		}
		fs := flag.NewFlagSet("apps add", flag.ContinueOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, appsHelp) }
		var c apps.Custom
		fs.IntVar(&c.Port, "port", 0, "the port the app answers on, on this machine")
		fs.StringVar(&c.Path, "path", "", "where to land, like /web (with --port)")
		fs.StringVar(&c.URL, "url", "", "a web address to link to instead")
		fs.StringVar(&c.Description, "description", "", "a line on what it's for")
		name, rest := splitFolder(args[1:])
		if err := parseFlags(fs, rest); err != nil {
			return err
		}
		if name == "" && fs.NArg() == 1 {
			name = fs.Arg(0)
		} else if name == "" || fs.NArg() != 0 {
			return usagef("usage: jag apps add <name> [--port <port> | --url <address>] [--description <text>]\n" +
				"put a name or description with spaces in quotes: jag apps add \"House chat\" --port 3000")
		}
		c.Name = name
		if c.Port == 0 && c.URL == "" && c.Description == "" {
			return fmt.Errorf("say what %q is: --port <port>, --url <address> or --description <text>", name)
		}
		if err := apps.AddCustom(dir, c); err != nil {
			return err
		}
		switch {
		case c.URL != "":
			fmt.Printf("added %q, a link to %s\n", strings.TrimSpace(c.Name), c.URL)
		case c.Port != 0:
			fmt.Printf("added %q on port %d; it shows while the port answers\n", strings.TrimSpace(c.Name), c.Port)
		default:
			fmt.Printf("%q gets the description %q when Jaggery finds it on this machine\n", strings.TrimSpace(c.Name), strings.TrimSpace(c.Description))
		}
		fmt.Println("every page in the house shows it within a minute")
		return nil
	case "remove", "rm":
		if len(args) != 2 {
			return usagef("usage: jag apps remove <name>")
		}
		if err := apps.RemoveCustom(dir, args[1]); err != nil {
			if errors.Is(err, apps.ErrNoCustom) {
				return fmt.Errorf("no app called %q was added on this machine; `jag apps` lists them", args[1])
			}
			return err
		}
		fmt.Printf("removed %q\n", args[1])
		return nil
	case "name":
		if err := straightQuotes(args[0], args[1:]); err != nil {
			return err
		}
		return nameApp(dir, args[1:])
	case "list", "ls":
		return listApps()
	case "-h", "--help", "help":
		fmt.Print(appsHelp)
		return nil
	}
	return usagef("unknown apps command %q\n\n%s", args[0], appsHelp)
}

func listApps() error {
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	local, err := apps.LoadCustom(dir)
	if err != nil {
		return err
	}
	fmt.Println("added on this machine:")
	if len(local) == 0 {
		fmt.Println("  nothing (jag apps add <name> --port <port>)")
	}
	for _, c := range local {
		fmt.Printf("  %-20s %-28s %s\n", c.Name, customWhere(c), c.Description)
	}
	names, err := apps.LoadNames(dir)
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Printf("  %-20s %-28s %s\n", n.App, "name "+n.Name+".local", "")
	}

	snap := loadHouse()
	clashes := nameClashes(snap)
	fmt.Println("\non the page:")
	shown := 0
	for _, m := range snap.All() {
		if m.Name == "" || (!m.Self && !m.Reachable) {
			continue
		}
		for _, a := range m.Inventory.Apps {
			where := fmt.Sprintf("%s.local:%d%s", strings.ToLower(m.Name), a.Port, a.Path)
			named := ""
			switch {
			case a.URL != "":
				where = a.URL
			case a.LocalName != "":
				named = fmt.Sprintf(", also %s", where)
				where = a.LocalName + ".local" + a.Path
				if others := clashes[a.LocalName]; len(others) > 1 {
					named += " (" + a.LocalName + " is given on " + strings.Join(others, " and ") + "; remove one)"
				}
			}
			fmt.Printf("  %-20s %-28s from %s%s\n", a.Name, where, m.Name, named)
			shown++
		}
	}
	if shown == 0 {
		fmt.Println("  nothing yet (is the agent running? apps are checked once a minute)")
	}
	return nil
}

// nameApp is jag apps name <app> <name> and jag apps name <app> --remove.
func nameApp(dir string, args []string) error {
	remove := false
	var rest []string
	for _, a := range args {
		if a == "--remove" || a == "-remove" {
			remove = true
			continue
		}
		rest = append(rest, a)
	}
	usage := usagef("usage: jag apps name <app> <name>, or jag apps name <app> --remove\n" +
		"put an app's name with spaces in quotes: jag apps name \"Open WebUI\" chat")
	if remove {
		if len(rest) != 1 {
			return usage
		}
		if err := apps.RemoveName(dir, rest[0]); err != nil {
			if errors.Is(err, apps.ErrNoName) {
				return fmt.Errorf("%q has no name on this machine; `jag apps` lists the names", rest[0])
			}
			return err
		}
		fmt.Printf("%q has no name now; every page in the house shows that within a minute\n", rest[0])
		return nil
	}
	if len(rest) != 2 {
		return usage
	}
	app, n := strings.TrimSpace(rest[0]), apps.NormalName(rest[1])
	if err := apps.CheckName(n); err != nil {
		return err
	}
	snap := loadHouse()
	a, ok := ownApp(snap, app)
	if !ok {
		return fmt.Errorf("no app called %q on this machine; `jag apps` lists them (is the agent running? apps are checked once a minute)", app)
	}
	if a.URL != "" {
		return fmt.Errorf("%q is a link to %s, not an app on this machine, so it can't have a name", a.Name, a.URL)
	}
	if why := nameTaken(snap, a.Name, n); why != "" {
		return errors.New(why)
	}
	if err := apps.SetName(dir, a.Name, n); err != nil {
		return err
	}
	fmt.Printf("%q is named %s.local; every page in the house shows it within a minute\n", a.Name, n)
	return nil
}

// ownApp finds the app called name (any case) on this machine's page.
func ownApp(snap house.Snapshot, name string) (apps.App, bool) {
	for _, a := range snap.Self.Inventory.Apps {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return apps.App{}, false
}

// nameTaken says why n can't name app on this machine, or "" if it can: it
// is a machine's name, or another app in the house has it. Machines that are
// away still count, since their apps keep their names when they come back.
func nameTaken(snap house.Snapshot, app, n string) string {
	for _, m := range snap.All() {
		if strings.EqualFold(m.Name, n) || strings.EqualFold(m.Inventory.Hostname, n) {
			return fmt.Sprintf("%s.local is the machine %s; choose another name", n, m.Name)
		}
	}
	for _, m := range snap.All() {
		for _, a := range m.Inventory.Apps {
			if a.LocalName != n || (m.Self && strings.EqualFold(a.Name, app)) {
				continue
			}
			if m.Self {
				return fmt.Sprintf("%s is already the name of %s on this machine; remove it first: jag apps name %q --remove", n, a.Name, a.Name)
			}
			return fmt.Sprintf("%s is already the name of %s on %s; choose another, or remove it there first: jag apps name %q --remove (on %s)",
				n, a.Name, m.Name, a.Name, m.Name)
		}
	}
	return ""
}

// nameClashes maps each app name to the machines that give it. Two machines
// naming at the same moment can both succeed; more than one entry is a clash.
func nameClashes(snap house.Snapshot) map[string][]string {
	out := map[string][]string{}
	for _, m := range snap.All() {
		seen := map[string]bool{}
		for _, a := range m.Inventory.Apps {
			if a.LocalName != "" && !seen[a.LocalName] {
				seen[a.LocalName] = true
				out[a.LocalName] = append(out[a.LocalName], m.Name)
			}
		}
	}
	return out
}

func customWhere(c apps.Custom) string {
	switch {
	case c.URL != "":
		return c.URL
	case c.Port != 0:
		return fmt.Sprintf("port %d%s", c.Port, c.Path)
	default:
		return "(description)"
	}
}

// straightQuotes catches curly quotes, which notes and chat apps put in place
// of straight ones: the shell doesn't treat them as quotes, so a name with a
// space arrives as several words with the curly marks still on.
func straightQuotes(sub string, args []string) error {
	for _, a := range args {
		if strings.ContainsAny(a, "\u201c\u201d\u2018\u2019") {
			fixed := make([]string, len(args))
			for i, b := range args {
				fixed[i] = strings.NewReplacer("\u201c", "\"", "\u201d", "\"", "\u2018", "'", "\u2019", "'").Replace(b)
			}
			return fmt.Errorf("these are curly quotes (\u201c \u201d), which the shell doesn't read as quotes; type straight ones (\"):\n  jag apps %s %s",
				sub, strings.Join(fixed, " "))
		}
	}
	return nil
}
