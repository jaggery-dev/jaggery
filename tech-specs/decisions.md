# Decisions and lessons

Why Jaggery works the way it does: the choices that shaped it and the measurements behind them, from building it and running it on real machines (October 2026). Read the section for the area you're changing before proposing something different; most of these were learnt the hard way. Each section names the code it applies to.

## How we test

- **Start network debugging with a positive control.** Three early findings were wrong in the same way: an empty answer was read as a broken network, when nothing was publishing the thing being asked for. Ask for something that definitely exists (another device's mDNS service) before concluding the path is broken.
- **Try the simplest version on real use first,** and test anything that judges traffic on captured real traffic, never only on synthetic examples (see "Content" below).
- **A slice is done when it works on real machines,** not when its tests pass. The test house (`scripts/house-test.sh`) runs many agents on one Linux machine in network namespaces, with a fake release server and a fake app, and catches most regressions before hardware does.

## Discovery and names (`internal/mdns`, `internal/discovery`)

- **Our own thin mDNS layer over `miekg/dns`,** because the libraries didn't let us control the sockets, and the sockets were the whole problem:

  | Platform | Socket options | Why |
  |---|---|---|
  | Linux | `SO_REUSEADDR` only | Every socket in the group gets a copy; `SO_REUSEPORT` would load-balance packets away from avahi. |
  | macOS / BSD | `SO_REUSEADDR` and `SO_REUSEPORT` | Without `SO_REUSEPORT`, binding 5353 fails because `mDNSResponder` holds it. |

  Both also need the outgoing multicast interface set explicitly (`IP_MULTICAST_IF`). On macOS a socket bound to `0.0.0.0` has no route for `224.0.0.0/4`, so without it every send, including every answer, fails silently.
- **Don't announce on overlay interfaces** (`tailscale0`, `wg*`, `tun*`, `zt*`, `docker0`, `veth*`). Announcing a VPN address for a `.local` name makes it unreachable for everyone not on that VPN.
- **Every published name carries an NSEC record saying "A only"** (RFC 6762 §6.1). Without it macOS waits 2–5 s for an IPv6 answer that never comes; with it, lookups take milliseconds.
- **One elected machine answers for `jaggery.local` and `ai.local`:** it prefers machines that serve port 80 and aren't laptops, and among those takes the lowest machine id. Every agent computes the same answer, so no extra protocol is needed.
- **An isolated machine must back off, not shout louder.** A machine that can't reach its peers elects itself, and out-announcing it didn't work. It worked when a machine holds a name only while it hears nobody else claim it, probes silently before claiming, doesn't take a goodbye as "the name is free", and lets "contested" expire 60 s after the last claim. Measured: 18 out of 18 lookups went to the right holder, against 4 out of 18 with no handling.
- **Safari needs `http://`** for a `.local` name it hasn't visited (it tries HTTPS first, then `www.`). Bare names in a browser need local HTTPS, which is on the roadmap.
- **Laptops sleep.** A sleeping Mac still answers ping through the Bonjour sleep proxy while mDNS goes quiet, so laptops never hold the shared names, and the page should show "asleep" rather than "crashed".

## Firewalls and ports (`internal/reach`, `scripts/install.sh`)

- **The host firewall, not mDNS, is the first-run hazard.** ufw's defaults let mDNS through and drop every service port, so the name resolves and the page doesn't load. The agent asks an approved peer to dial back its own ports and says exactly what to type when one is blocked. The installer opens Jaggery's ports to the local subnet when `ufw` or `firewalld` is active.
- **Port 80 is free on macOS and needs a capability on Linux** (`AmbientCapabilities=CAP_NET_BIND_SERVICE` in the unit, never root). Everything also works on 7373, for when 80 is taken (a NAS's own UI).
- **"Obvious" ports are often taken** on a machine that already does something. Never assume one is free.

## macOS (`scripts/install.sh`, macOS mode)

- **Local Network privacy blocks a process no session owns.** An agent started with `nohup` from a shell that then closed lost the LAN entirely (`sendmsg: no route to host`), then elected itself, because it couldn't hear anyone.
- **A LaunchDaemon running the bare binary as the user works:** macOS asks once for Local Network permission (someone must click Allow), and the permission survives self-updates and reboots. It isn't exempt from the prompt, whatever the documentation suggests.
- **At boot, the network may not be up yet.** The agent waits for a usable interface instead of exiting.

## Routing (`internal/router`, `internal/inventory`)

- **Free memory decides whether a machine *can* hold a model, never which machine is *best*.** Measured on the same prompt: a CPU machine with 16.7 GiB free ran at 7.3 tokens/s, a laptop GPU with 5.3 GiB free at 41.5 tokens/s. Ranking by free memory picked the machine 4.9× slower. Order: a machine that already has the model loaded; then, among machines with room for it, the best tokens/s observed; then a static weight per runtime (GPU above CPU) until there's a measurement.
- **Use the memory budget the runtime reports, not the RAM on the box.** An 8 GB Apple-silicon machine gives its GPU about 5.3 GiB.
- **A new model isn't served until a person approves it,** and approval pins its files, so a model swapped on disk is noticed.

## Trust (`internal/identity`, `internal/house`)

- **Joining needs a one-time code or a person's approval.** Seeing a machine on the network never makes it trusted.
- **Machines pin each other's keys and talk over mutual TLS;** each signs the list of machines it trusts, so a machine joined through one member is trusted by the rest.
- **The home network is trusted** (no accounts, no per-person keys), like a printer. What that allows is written down in [docs/security.md](../docs/security.md), and the review of what can still reach the house from outside that trust (web pages, the internet, devices claiming the shared names) is in [security-local.md](security-local.md).

## Updates (`internal/update`, `cmd/jag/autoupdate.go`, `cmd/jag/relay.go`)

- **Updates came first.** Until merges reached every machine with nobody touching them, every check on real hardware waited on someone with `sudo` and `scp`.
- **Releases are signed in CI with an ed25519 key,** and `jag` carries two public keys, the current one and a spare, so a leaked key can be replaced without stranding machines. A build is installed only if its signature and checksum hold, including when it comes through another machine. Details in [releases.md](releases.md).
- **Relays:** a machine that can reach GitHub keeps every platform's build for the house, and the others can fetch from it. A machine fetches only from peers a person approved on it directly, not from ones another machine vouched for.
- **Roll out one machine at a time, from a proven build.** The name holder installs first. The others install the build it has *run healthily*, not the newest published, because a stream of merges otherwise restarts their wait forever. One machine on trial at a time. A build that fails to start three times is rolled back, and the whole house skips it. A withdrawn release is skipped, and machines running it move off it.
- **A check that finds nothing new costs GitHub one request,** because GitHub allows 60 unauthenticated requests an hour per address, shared by the whole house.
- **Reuse connections to peers.** Building a new HTTP transport for every call leaked about 2,300 sockets an hour, until a Mac hit its per-process limit of 10,240 and answered nothing. One transport per peer fixed it. Also: try each of a peer's addresses, because one stale address must not hide a machine.

## Agents and content (`cmd/jag/mcp.go`, `internal/shares`, `internal/hosted`)

- **Agents read; people decide.** Nothing an API caller or MCP client can reach approves models, trusts machines, adds shares, changes update policy or adds hosted providers. Those stay at a terminal. Writable shares are the one exception: opt-in per share, with every change kept 30 days and undoable.
- **Jaggery doesn't inspect request or file content to judge it.** A gate that looked for personal data in requests bound for hosted models (rules for identifiers and secrets, then a local model detector, then warn-and-confirm) was built, tried on real use, and removed:
  - Agent requests are mostly text nobody typed: system prompts, skills, tool descriptions and tool results. The model detector read words in them as names, addresses and account ids, and raised almost nothing but false alarms.
  - Rules, even precise ones, fired constantly on what coding agents handle every day: fixtures, configs and code full of emails, numbers and keys.
  - A synthetic test corpus of short messages had said the detector worked. Real traffic looked nothing like it.
  - A warning that's usually wrong teaches people to click through it.

  What a household controls well instead is *where* data goes and *what's recorded*. So hosted providers are opt-in, allow-listed per model, capped in spend, and every request that leaves is kept word for word for 30 days (`jag outbound`). Before proposing anything that inspects content, test it on captured real agent traffic.
- **Prompt injection is a risk we document, not one we filter.** See [docs/security.md](../docs/security.md#what-is-not-protected-yet).
