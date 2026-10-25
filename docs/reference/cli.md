# Commands

Generated from `jag help` by `scripts/gen-cli-docs.sh`; edit the usage text in `cmd/jag`, not this file.

```
jag - every machine in your house, one system

usage:
  jag up              start the agent on this machine
  jag status          show this machine's identity and the peers it can see
  jag peers           list known peers and whether they are approved
  jag map             show every machine in the house and its hardware
  jag invite          print a one-time code for adding a new machine
  jag join <code>     join the house with a code from jag invite (no address needed)
  jag join <code> --at <address>  join through a machine beyond this network (a VPN, another subnet)
  jag join <address>  reach a machine by address, verify it, and trust it
  jag approve <id>    approve a pending peer (id or a unique prefix)
  jag ping <id>       reach an approved peer over mutual TLS
  jag forget <id>     remove a peer from this machine's list
  jag models          every model in the house; approve new ones (jag models approve <name>)
  jag doctor          check this machine and its network, and say how to fix what fails
  jag activity [day]  who used which AI model, on which machine (default today)
  jag outbound [day]  what left the house for hosted models (jag outbound show <id>)
  jag apps            the apps on the page; add your own (jag apps add <name> --port <port>)
  jag share           folders this machine lends to models and agents (jag share add <folder>)
  jag files [log]     what models and agents read and changed in shares (jag files restore)
  jag version         show this build's version
  jag licences        the licences of Jaggery and the software built into it
  jag update          install the newest release (--check, --rollback)

jag <command> --help says more about a command; jag help exit-codes lists
what jag's exit codes mean. Commands that only show something take --json.
```

## jag up

```
usage: jag up

Starts the agent on this machine and keeps it running until stopped. It finds
the other machines in the house, shares what this machine has with the ones
you approved, and takes its turn answering for jaggery.local and ai.local. The
first run creates this machine's identity. Installed as a service, the agent
starts by itself; run jag up by hand to try Jaggery out.
```

## jag status

```
usage: jag status

Shows this machine's name, id, fingerprint and hardware, and the machines it
can see on the network right now.
```

## jag peers

```
usage: jag peers

Lists the machines this one knows, and whether each is approved, pending or
unverified. Approve a pending one with jag approve <id>. When two machines use
one name, both are listed with their address and key fingerprint: approve the
one whose fingerprint matches jag status on that machine.
```

## jag map

```
usage: jag map

Shows every machine in the house: whether it's on, its memory, disks and GPU,
and the AI runtimes and models it has. It reads what the running agent last
collected, so start jag up first.
```

## jag invite

```
usage: jag invite

Prints a one-time code for adding a machine to the house. On the new machine,
run jag join with the code, for example
jag join maple-river-otter-candle-quartz-fig. The code works once, for 10
minutes.
```

## jag join

```
usage: jag join <code>                  join the house with a code from jag invite
       jag join <code> --at <address>   join through a machine beyond this network (a VPN, another subnet)
       jag join <address>               reach a machine by address, check its key, and approve it

With a code, no address is needed: the machine that made the code is found on
the network. Use --at when it isn't, for example jag join <code> --at 192.0.2.10.
Joining by address alone prints the other machine's fingerprint: check it
matches what jag status says there.
```

## jag approve

```
usage: jag approve <id>

Approves a pending machine: this machine lets it in, and the rest of the house
trusts it too. The id is the one jag peers shows, or the start of it if no
other id begins the same way, for example jag approve 1a2b. If another
machine uses the same name, it says so and shows both fingerprints.
```

## jag ping

```
usage: jag ping <id>

Reaches an approved machine over its secure connection and shows what it
reports. Use it to check two machines trust each other, for example
jag ping 1a2b.
```

## jag forget

```
usage: jag forget <id>

Removes a machine from this machine's list, for example jag forget 1a2b, and
remembers that it was removed so another machine's list doesn't approve it
again. Run it on each machine when one has left the house for good.
```

## jag models

```
usage: jag models                         every model in the house, and whether it's approved
       jag models approve <name> [digest]  let the router serve a model (its current files)
       jag models enforce on|off            refuse unapproved models (on), or serve every model

Approval is house-wide: approve on any machine and the others follow within
seconds. It pins the model's digest, so if the files under a name change, the
model waits for approval again.
```

## jag doctor

```
usage: jag doctor

Checks this machine and its network: its identity, the agent, multicast, the
names jaggery.local and ai.local (and whether anything outside the house
answers for them), port 80, whether approved machines can reach it, its AI
runtimes, the clock and updates. Each check that fails says how to
fix it.

Exits 0 when every check passed, 4 when some only warned, 5 when any failed.
```

## jag activity

```
usage: jag activity [day]

Who used which AI model, on which machine, across the house. The day looks
like 2026-10-07; without one it shows today. Needs the agent running here.
```

## jag outbound

```
usage: jag outbound [day]       what left the house for hosted models (default today)
       jag outbound show <id>    the request that was sent, in full

Every request to a hosted provider is logged on the machine that sent it,
including the ones held back or refused. The day looks like 2026-10-07.
```

## jag apps

```
usage: jag apps                                  the apps on the page, house-wide
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
```

## jag share

```
usage: jag share                                       folders lent to models and agents, house-wide
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
```

## jag files

```
usage: jag files log [day] [--share <name>]    what models and agents read and changed (default today)
       jag files versions <share> <path>       old copies of a file, newest first
       jag files restore <share> <path> [version]  put an old copy back (the newest by default)

The log is on each machine: calls on the shares it holds, and calls it passed
on to another machine. Run it on the machine that holds a share to see
everything done to it. Restore works on this machine's shares; the file it
replaces is kept as a version too, so a restore can be undone.
```

## jag version

```
usage: jag version

Shows this build's version, the platform, and the release channel it updates
from.
```

## jag update

```
usage: jag update [--check] [--channel beta|prod] [--rollback]
       jag update --policy auto|notify|off [--channel beta|prod]

Replaces this machine's agent with the newest release on its channel (beta
builds follow beta, prod builds follow prod). The running agent notices and
restarts itself into the new version within a few seconds.

The running agent also checks every 30 minutes. With --policy auto it
installs new releases itself: the machine answering for jaggery.local goes
first, the rest 30 minutes later, and a build that won't start is rolled back
and skipped by the whole house. notify only reports them; off stops checking.
Beta builds default to auto, prod and dev builds to notify.

Without GitHub access it asks a machine you approved here that keeps the
build. Releases are public and need no token. One is used if present
($JAGGERY_GITHUB_TOKEN, $GITHUB_TOKEN, a github-token file in the state
directory, or a logged-in gh), which raises GitHub's rate limit for a house
with many machines behind one address.
```

## jag licences

```
usage: jag licences

Prints the licence of Jaggery (Apache-2.0) and of the software built into
jag: Go and each library, with their copyright notices. jag licenses works
too.
```

## Exit codes

```
usage: jag help exit-codes

What jag's exit codes mean, for scripts and agents:

  0  done; for jag doctor, every check passed
  1  failed: the error on stderr says why and how to fix it
  2  wrong usage: an unknown command, flag or argument
  3  only test builds that crash on purpose; releases never use it
  4  jag doctor: nothing failed, but at least one check warned
  5  jag doctor: at least one check failed
```

Files, ports and environment variables are in [files-and-ports.md](files-and-ports.md).
