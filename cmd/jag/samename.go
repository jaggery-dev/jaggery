package main

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
)

// namedMachine is one of several machines using the same name: a
// device can announce itself as "nas" and wait to be approved, so where
// approval is offered each one's address and key fingerprint are shown.
type namedMachine struct {
	MachineID   string
	Name        string
	Trust       string // "this machine" for this one
	Addrs       []netip.Addr
	Port        int
	Fingerprint string // empty until its key is proven
}

// sameNames groups the machines that share a name, ignoring case. This
// machine is one of them when a peer uses its name. id may be nil.
func sameNames(id *identity.Identity, peers []home.Peer) [][]namedMachine {
	byName := map[string][]namedMachine{}
	if id != nil {
		byName[strings.ToLower(id.Name)] = []namedMachine{{MachineID: id.ID, Name: id.Name, Trust: "this machine", Fingerprint: id.Fingerprint()}}
	}
	for _, p := range peers {
		m := namedMachine{MachineID: p.MachineID, Name: p.Name, Trust: string(p.Trust), Addrs: p.Addrs, Port: p.Port}
		if p.Verified() {
			m.Fingerprint = identity.FingerprintFromKey(ed25519.PublicKey(p.PublicKey))
		}
		k := strings.ToLower(p.Name)
		byName[k] = append(byName[k], m)
	}
	var out [][]namedMachine
	for _, group := range byName {
		if len(group) > 1 {
			out = append(out, group)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i][0].Name) < strings.ToLower(out[j][0].Name) })
	return out
}

// sharesName reports whether machineID is in one of groups.
func sharesName(groups [][]namedMachine, machineID string) []namedMachine {
	for _, g := range groups {
		for _, m := range g {
			if m.MachineID == machineID {
				return g
			}
		}
	}
	return nil
}

// printSameName says that several machines use one name, and how to tell
// them apart.
func printSameName(w io.Writer, group []namedMachine) {
	fmt.Fprintf(w, "%d machines call themselves %q:\n", len(group), group[0].Name)
	for _, m := range group {
		where := "no address yet"
		if m.Trust == "this machine" {
			where = "here"
		} else if len(m.Addrs) > 0 {
			where = "at " + netip.AddrPortFrom(m.Addrs[0], uint16(m.Port)).String()
		}
		fp := "fingerprint not proven yet"
		if m.Fingerprint != "" {
			fp = "fingerprint " + m.Fingerprint
		}
		fmt.Fprintf(w, "  %s  %s, %s (%s)\n", m.MachineID, where, fp, m.Trust)
	}
	fmt.Fprintln(w, "Only approve the one whose fingerprint matches what `jag status` shows on that machine;")
	fmt.Fprintln(w, "jag forget <id> removes one that isn't yours. Joining with a code (jag invite) avoids the question.")
}
