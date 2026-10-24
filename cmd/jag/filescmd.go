package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/shares"
)

const filesHelp = `usage: jag files log [day] [--share <name>]    what models and agents read and changed (default today)
       jag files versions <share> <path>       old copies of a file, newest first
       jag files restore <share> <path> [version]  put an old copy back (the newest by default)

The log is on each machine: calls on the shares it holds, and calls it passed
on to another machine. Run it on the machine that holds a share to see
everything done to it. Restore works on this machine's shares; the file it
replaces is kept as a version too, so a restore can be undone.
`

func files(args []string) error {
	if len(args) == 0 {
		return filesLog(nil)
	}
	switch args[0] {
	case "log":
		return filesLog(args[1:])
	case "versions":
		return filesVersions(args[1:])
	case "restore":
		if jsonOutput {
			return errJSONNotHere("jag files restore")
		}
		return filesRestore(args[1:])
	case "-h", "--help", "help":
		fmt.Print(filesHelp)
		return nil
	}
	return usagef("unknown files command %q\n\n%s", args[0], filesHelp)
}

func filesLog(args []string) error {
	fs := flag.NewFlagSet("files log", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, filesHelp) }
	share := fs.String("share", "", "only this share")
	day := ""
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		day, args = args[0], args[1:]
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	when, err := activity.ParseDay(day, time.Now())
	if err != nil {
		return err
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	entries, err := shares.ReadLog(dir, when)
	if err != nil {
		return err
	}
	if jsonOutput {
		list := []shares.LogEntry{}
		for _, e := range entries {
			if *share == "" || e.Share == *share {
				list = append(list, e)
			}
		}
		return emitJSON(map[string]any{"day": when.Format("2006-01-02"), "entries": list})
	}
	n := 0
	for _, e := range entries {
		if *share != "" && e.Share != *share {
			continue
		}
		n++
		fmt.Printf("%s  %s\n", e.Time.Local().Format("15:04:05"), logLine(e))
	}
	if n == 0 {
		fmt.Printf("no calls on shares on %s\n", when.Format("2006-01-02"))
	}
	return nil
}

func filesVersions(args []string) error {
	if len(args) != 2 {
		return usagef("usage: jag files versions <share> <path>")
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	vs, err := shares.Versions(dir, args[0], args[1])
	if err != nil {
		return err
	}
	if jsonOutput {
		if vs == nil {
			vs = []shares.Version{}
		}
		return emitJSON(map[string]any{"share": args[0], "path": args[1], "versions": vs})
	}
	if len(vs) == 0 {
		fmt.Printf("no old versions of %s in %s\n", args[1], args[0])
		return nil
	}
	for _, v := range vs {
		fmt.Printf("%s  %s  %s\n", v.Name, v.At.Local().Format("2006-01-02 15:04:05"), sizeText(v.Size))
	}
	return nil
}

func filesRestore(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return usagef("usage: jag files restore <share> <path> [version]")
	}
	version := ""
	if len(args) == 3 {
		version = args[2]
	}
	dir, err := identity.StateDir()
	if err != nil {
		return err
	}
	resp, err := shares.Restore(dir, args[0], args[1], version, time.Now())
	recordFileCall(dir, shares.Request{Op: shares.OpRestore, Share: args[0], Path: args[1]}, resp, err, fileCaller{App: "jag files restore", From: "this machine's terminal"})
	if err != nil {
		return err
	}
	fmt.Printf("restored %s in %s (%s)\n", resp.Path, args[0], sizeText(resp.Written))
	if resp.Version != "" {
		fmt.Println("the file it replaced is kept as a version: jag files versions lists it")
	}
	return nil
}
