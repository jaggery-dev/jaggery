# Rogue apps and web pages against the house

**Review, 2026-10-18.** What a hostile web page, a hostile device on the network, or a hostile program on one of the house's machines can do to a Jaggery house, what stops it today, and what's left. Written before going public; the user-facing summary is in [docs/security.md](../docs/security.md).

## The trust model (decided 2026-10-05, confirmed 2026-10-18)

Jaggery trusts the home network and the people on it. No accounts, no sign-in, no per-person keys: anyone on the network can use `ai.local`, read the shares, see the page and spend the hosted allowance up to its cap. It's made for a household or a small group.

So the review isn't "can someone on the Wi-Fi use the models": they can, by design. It's about **who gets that trust without being in the house**, and about where the trust is wider than a household would expect.

## The attackers

| Who | Where they stand | Example |
|---|---|---|
| A **web page** | In a browser on a device in the house | An ad, a compromised blog, a phishing page |
| The **internet** | Outside the house | A scanner sweeping IPv6 or a cloud provider's range |
| A **rogue device** | On the home network | A cheap camera, a smart TV, a guest's infected laptop |
| A **rogue program** | On a machine in the house | Another account on a shared machine, a malicious package, a container |
| A **rogue model or file** | Content Jaggery passes along | A model with code-running weights, a file in a share that carries instructions for an AI |

## What we found, and what was done

### 1. Web pages could read and write through the agent (fixed, `internal/languard`)

The agent's page port (80 and 7373) answered any request. A browser stops a page from *reading* cross-origin answers, but:

- **Cross-site requests.** A page could send `POST http://ai.local/v1/chat/completions` with a `text/plain` body (a "simple" request, no preflight). The router parsed it, and if the model was a hosted one, the house paid for it and logged the payload. Only `/mcp` had cross-origin protection.
- **DNS rebinding.** A page on `rebind.example.com` re-points that name at `192.168.1.20` after loading. Its requests to the agent are now same-origin to the browser, so it can read every answer: `/v1/house` (every machine, address, app and model), `/v1/house/activity` (who used what, when), and drive `/mcp` (list, read and search every share, and write the writable ones). `/mcp`'s cross-origin check passed, because to the browser the request *was* same-origin. This was the worst finding: any web page anyone in the house opened could read the shared folders.

**Fix.** The whole LAN port sits behind `languard`:

- **Host allow-list.** The `Host` header must be an IP address, a single-word name, `localhost`, or end in a local-only suffix (`.local`, `.lan`, `.home`, `.home.arpa`, `.internal`, `.localdomain`, `.localhost`); `JAGGERY_ALLOW_HOSTS` adds names. A rebinding page can only use a public domain, and public DNS can't serve those suffixes. This is the standard defence (Plex, Syncthing and Home Assistant all check `Host`).
- **Cross-origin protection everywhere.** `net/http.CrossOriginProtection` on every handler: a browser request that changes something (`POST`, `PUT`, `DELETE`, `PATCH`) with `Sec-Fetch-Site: cross-site` or a mismatched `Origin` is refused. Apps that aren't browsers send neither header and pass.
- **No CORS headers**, anywhere, so a page can't read answers. The router builds its own response headers, so a runtime's CORS headers aren't passed through. The guard's test checks it adds none.
- **Headers:** `X-Frame-Options: DENY` (no clickjacking of the page), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`.

The test house checks a rebound `Host` and a cross-origin `POST` are refused, and that a local name is served.

Browsers are also closing this from their side (Chrome's Private Network Access, now a "local network access" permission prompt), but not every browser, and not for older ones. We don't rely on it.

### 2. The internet could reach a machine with a public address (fixed, `internal/languard`)

The agent listens on every interface (`:7373`, `:80`). A home machine with a global IPv6 address and no inbound firewall (macOS has none on by default, and not every ISP router blocks inbound IPv6) answered the whole internet: the models, the shares, the page. So did a cloud machine joined with `jag join --at`.

**Fix.** The LAN port serves only requests from loopback, private (RFC 1918, IPv6 ULA), link-local, CGNAT and overlay (`100.64.0.0/10`, Tailscale) addresses, from addresses on this machine's own interface prefixes (a global IPv6 neighbour on the same home network), and from approved machines' addresses. `JAGGERY_ALLOW_FROM` adds prefixes. Refusals are logged once per source and reason every ten minutes.

The mutual-TLS port (7374) isn't filtered: it already refuses any caller without an approved key.

**Not done, on purpose:** binding to the LAN interfaces only. Interfaces and addresses come and go (DHCP, IPv6 privacy addresses, Wi-Fi to Ethernet), and rebinding listeners when they do is fiddly. The address check gets the same result.

### 3. Copyable commands on the page could carry a command of their own (fixed)

The page shows commands to paste, such as `jag models approve <name>`. Model names come from runtimes; LM Studio's and llama.cpp's are file names and can hold anything. A model file named `x; curl … | sh` produced a command that ran that too. The page now shell-quotes names in commands (`shq`). Text on the page was already HTML-escaped everywhere (`esc`).

## What a rogue device on the network can do (by design, now written down)

Anything a person in the house can. docs/security.md now says so, and tells people to put guests and smart devices on a separate network. Specifically:

| It can | Limited by |
|---|---|
| Use the models | Model approval: only models the house approved are served |
| Spend a hosted allowance | The cap and allow-list in `providers.json`; every request is in the outbound log with its source address |
| Read every share; change writable ones | Shares are read-only unless added `--writable`; dotfiles, `*.key`, `*.pem` never served; every change kept 30 days and undoable; every access logged |
| See the page and the activity | It shows inventory and metadata, never prompts or file contents |
| Join the house | No: it needs a one-time code (six words, single use, 10 minutes, 5 attempts) or a person's `jag approve` |
| Serve code to the house as an update | No: builds must carry a signature from a key built into `jag` |
| Get its own models served by the router | No: the router only reaches approved machines over mutual TLS |
| Read traffic between agents | No: mutual TLS |
| Read traffic between a phone and `ai.local` | Yes, if it can sniff the network: plain HTTP. On Wi-Fi, WPA2/3 keeps outsiders out; inside the network, everything is trusted |

### Done: rogue devices claiming the shared names

`ai.local` and `jaggery.local` are mDNS names. Any device can answer for them, and one that does gets the requests sent there: prompts, files written through `/mcp`, the hosted allowance's traffic. The agent notices a rival claim today (it uses it to elect the holder) but treats every rival as another Jaggery machine. **Proposed:** when a claim comes from an address that isn't a machine in the house, alert on the page and in `jag doctor` ("Something at 192.0.2.50 also answers for ai.local"). Doesn't stop it, but nothing on mDNS can. Apps holding secrets can use the machine's own name. **Built:** every agent listens for A records for the shared names from addresses other than its own (`mdns.WatchClaims`); the snapshot lists those that aren't an approved machine's for 10 minutes after they were last heard (`name_claims`, naming a Jaggery machine that isn't approved), and the agent's log, `jag doctor` ("name claims", a failure) and the page show them. App names aren't watched yet.

### Done: a rogue device naming itself after one of yours

A device can announce itself as `nas` and wait to be approved with `jag approve`. Joining by code (`jag invite`/`jag join`) isn't affected: the code proves which machine is which. **Proposed:** when two machines claim the same name, say so wherever approval is offered, and show each one's address and key fingerprint. **Built:** names are compared ignoring case, this machine's own name included; `jag peers` and `jag approve` print each machine's id, address, fingerprint (or "not proven yet") and trust, `jag peers --json` marks them `same_name`, and the page shows a card. Snapshot machines carry `fingerprint`.

### Low: fake apps under Tools

App fingerprints are a probe and a marker string; a device can answer like Jellyfin and appear under Tools with a link, a phishing page waiting for a Jellyfin password. Low value to an attacker who's already on the network. Note only.

## What a rogue program on a house machine can do

- **As the same user as the agent: everything.** It can read the identity key, the state directory, the outbound log. Nothing in Jaggery can defend against its own user; that's the operating system's line.
- **As another account on the same machine** (a shared family computer, a service account): the state directory is `0700` and its files `0600`, so not the key. But loopback is trusted like the LAN: it can use `ai.local`, the shares and the hosted allowance through `127.0.0.1:7373`. Consistent with the trust model, now noted here; docs say "everything on your home network", which includes other accounts.
- **In a container on a house machine:** Docker's bridge (`172.17.0.0/16`) is private, so containers are trusted too. That's how a chat app running in Docker reaches `ai.local`. A container you run is something you chose to trust.
- **Pretending to be an AI runtime:** runtimes are only detected on `127.0.0.1`, so only a local program can, by taking Ollama's port while Ollama is stopped. Its models then appear, but new models wait for approval and approval pins their files, so it can't get prompts for an approved model it doesn't have.
- **Attacking Ollama itself:** a web page can attack Ollama on `127.0.0.1:11434` directly, without Jaggery: Ollama decides which origins it accepts (`OLLAMA_ORIGINS`). If someone set `OLLAMA_ORIGINS=*`, any web page can use, pull and delete models. **Proposed:** `jag doctor` warns when a local runtime answers a cross-origin request with permissive CORS, next to the exposure check (G1). Issue to open.

## Rogue content

- **Models that run code:** formats that can run code when loaded (pickle-based files) are flagged on the page and wait for approval (G3/G4).
- **Prompt injection through shares and hosted replies:** a file in a share can carry instructions for an AI that reads it. Jaggery passes content through and doesn't inspect it (see "Agents and content" in [decisions.md](decisions.md)); the defences are read-only shares by default, undoable writes, and agents being read-only for house settings. Written up in docs/security.md (G7).
- **Jaggery's own tool descriptions** are fixed strings in the binary; a share's contents never become a tool description.

## Smaller notes

- **Denial of service:** the LAN port accepts request bodies up to 8 MB with no rate limit; anyone on the network can keep a runtime busy. Inside the trust model; no change.
- **The pairing proof endpoint** (`/v1/identity/prove`) signs anything a stranger sends, but only nonces of exactly 32 bytes; everything else the key signs (house rosters, TLS handshakes) is longer, so the signatures can't be reused.
- **The join endpoint** is reachable before trust, by design. Codes are 6 words from a 256-word list (48 bits), single use, live 10 minutes, and the invite is dropped after 5 wrong attempts. The code never crosses the network; each side proves it with an HMAC bound to its own key.

## Follow-ups

| What | Before going public? |
|---|---|
| Alert when a non-house device claims `ai.local`/`jaggery.local` | Yes |
| Duplicate machine names flagged where approval is offered | No |
| `jag doctor` warns on permissive CORS on a local runtime | No |
| Prompt-injection docs | Yes |
| Re-run this review when per-person keys (K1) or HTTPS on local names are designed | — |
