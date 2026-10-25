package main

import "strings"

// Usage for the commands that have no subcommands of their own. The rest
// (models, apps, share, files, update) keep theirs next to the code.
const (
	upHelp = `usage: jag up

Starts the agent on this machine and keeps it running until stopped. It finds
the other machines in the house, shares what this machine has with the ones
you approved, and takes its turn answering for jaggery.local and ai.local. The
first run creates this machine's identity. Installed as a service, the agent
starts by itself; run jag up by hand to try Jaggery out.
`
	statusHelp = `usage: jag status

Shows this machine's name, id, fingerprint and hardware, and the machines it
can see on the network right now.
`
	peersHelp = `usage: jag peers

Lists the machines this one knows, and whether each is approved, pending or
unverified. Approve a pending one with jag approve <id>. When two machines use
one name, both are listed with their address and key fingerprint: approve the
one whose fingerprint matches jag status on that machine.
`
	mapHelp = `usage: jag map

Shows every machine in the house: whether it's on, its memory, disks and GPU,
and the AI runtimes and models it has. It reads what the running agent last
collected, so start jag up first.
`
	inviteHelp = `usage: jag invite

Prints a one-time code for adding a machine to the house. On the new machine,
run jag join with the code, for example
jag join maple-river-otter-candle-quartz-fig. The code works once, for 10
minutes.
`
	joinHelp = `usage: jag join <code>                  join the house with a code from jag invite
       jag join <code> --at <address>   join through a machine beyond this network (a VPN, another subnet)
       jag join <address>               reach a machine by address, check its key, and approve it

With a code, no address is needed: the machine that made the code is found on
the network. Use --at when it isn't, for example jag join <code> --at 192.0.2.10.
Joining by address alone prints the other machine's fingerprint: check it
matches what jag status says there.
`
	approveHelp = `usage: jag approve <id>

Approves a pending machine: this machine lets it in, and the rest of the house
trusts it too. The id is the one jag peers shows, or the start of it if no
other id begins the same way, for example jag approve 1a2b. If another
machine uses the same name, it says so and shows both fingerprints.
`
	pingHelp = `usage: jag ping <id>

Reaches an approved machine over its secure connection and shows what it
reports. Use it to check two machines trust each other, for example
jag ping 1a2b.
`
	forgetHelp = `usage: jag forget <id>

Removes a machine from this machine's list, for example jag forget 1a2b, and
remembers that it was removed so another machine's list doesn't approve it
again. Run it on each machine when one has left the house for good.
`
	doctorHelp = `usage: jag doctor

Checks this machine and its network: its identity, the agent, multicast, the
names jaggery.local and ai.local (and whether anything outside the house
answers for them), port 80, whether approved machines can reach it, its AI
runtimes, the clock and updates. Each check that fails says how to
fix it.

Exits 0 when every check passed, 4 when some only warned, 5 when any failed.
`
	activityHelp = `usage: jag activity [day]

Who used which AI model, on which machine, across the house. The day looks
like 2026-10-07; without one it shows today. Needs the agent running here.
`
	outboundHelp = `usage: jag outbound [day]       what left the house for hosted models (default today)
       jag outbound show <id>    the request that was sent, in full

Every request to a hosted provider is logged on the machine that sent it,
including the ones held back or refused. The day looks like 2026-10-07.
`
	versionHelp = `usage: jag version

Shows this build's version, the platform, and the release channel it updates
from.
`
	licencesHelp = `usage: jag licences

Prints the licence of Jaggery (Apache-2.0) and of the software built into
jag: Go and each library, with their copyright notices. jag licenses works
too.
`
)

// helpFor returns the usage text for a command, with aliases.
func helpFor(cmd string) (string, bool) {
	switch cmd {
	case "exit-codes":
		return exitCodesHelp(), true
	case "up":
		return upHelp, true
	case "status":
		return statusHelp, true
	case "peers":
		return peersHelp, true
	case "map":
		return mapHelp, true
	case "invite":
		return inviteHelp, true
	case "join":
		return joinHelp, true
	case "approve":
		return approveHelp, true
	case "ping":
		return pingHelp, true
	case "forget":
		return forgetHelp, true
	case "models":
		return modelsHelp, true
	case "doctor":
		return doctorHelp, true
	case "activity":
		return activityHelp, true
	case "outbound":
		return outboundHelp, true
	case "apps", "app":
		return appsHelp, true
	case "share", "shares":
		return shareHelp, true
	case "files":
		return filesHelp, true
	case "version", "--version":
		return versionHelp, true
	case "update":
		return updateHelp, true
	case "licences", "licenses":
		return licencesHelp, true
	}
	return "", false
}

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

// helpRequested says whether args (os.Args without the program name) ask for
// a command's usage: jag join --help, jag join -h or jag help join.
func helpRequested(args []string) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	cmd := args[0]
	switch {
	case cmd == "help" || cmd == "-h" || cmd == "--help":
		cmd = args[1]
	case !isHelpFlag(args[1]):
		return "", false
	}
	return helpFor(strings.TrimSpace(cmd))
}
