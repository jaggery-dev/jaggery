# Jaggery: app names, `chat.local` for any app on the page (track W)

**Status: agreed 2026-10-21, with the proposed answer to each decision and question below; W1 to W3 built.** Track W in [the plan](plan.md); W1 to W3 are done; W4 is a week of use on real apps.

**Goal:** a person types `chat.local` instead of `desktop.local:3000`. The owner gives an app on the page a short name; every device in the house reaches the app by that name, without knowing which machine runs it or on which port.

**Done when:** on a house where Open WebUI runs on `desktop` at port 3000, the owner runs `jag apps name "Open WebUI" chat` on `desktop`; within a minute a phone on the Wi-Fi opens `http://chat.local`, signs in and chats (streaming answers included); the page links to `chat.local`; the app is moved to `nas` and `chat.local` keeps working once the name is given there.

## Why this belongs in Jaggery

Jaggery already does both halves for its own two names. One machine, the name holder, answers for `jaggery.local` and `ai.local` over mDNS ([how-it-works.md](../docs/how-it-works.md)), and the agent serves port 80 for them. Apps are already found (D5) or added by the owner (`jag apps add`), with their machine and port. App names add more names to the first half and a forwarding step to the second. Go's standard library has the forwarding (`net/http/httputil.ReverseProxy`, including WebSockets), so there is no new dependency, and nothing like a separate Caddy for the owner to configure.

It is naming, not managing apps: Jaggery doesn't install, start, configure or sign in to anything. It answers for a name and passes the request on, exactly as it does for `ai.local`.

## How it works

```
phone ── http://chat.local ──▶ name holder :80 ── languard ──▶ forward to desktop:3000 (the app, as found)
         (mDNS: chat.local = the name holder)
```

- **Names are given by a person, at the terminal of the machine that runs the app**, like shares and model approval: `jag apps name <app> <name>` and `jag apps name <app> --remove`. One compromised machine can't claim another machine's names. Nothing is named automatically: a house with two Open WebUIs would fight over `chat`.
- **Names travel in the inventory** with the app (name, machine, port), like share names today.
- **The name holder answers for every app name** over mDNS, as it does for `jaggery.local` and `ai.local`, and forwards requests whose `Host` is an app name to the app's machine and port.
- **Only apps the page would show:** the target must answer on its machine's LAN address, the same rule detection uses. Forwarding never reaches anything the house couldn't already reach, so it adds no exposure: `localhost`-only apps stay unreachable.
- **Collisions are refused** at `jag apps name` time if another machine already uses the name. Jaggery's own names (`jaggery`, `ai`) are reserved, and so is every machine's name in the house: `desktop.local` already belongs to the machine called `desktop`. Two machines naming at the same moment can still both win; the holder then answers for the name with the machine that sorts first by id, and the page and `jag apps` show the clash on both.
- **A name is one DNS label:** lower-case letters, digits and hyphens, 1 to 63 characters, not starting or ending with a hyphen.
- **Only apps with a port can be named.** A link added with `--url` points somewhere Jaggery doesn't run.
- **The page links to the name** instead of `machine.local:port` when one is set.

## Decisions

Each was agreed as proposed on 2026-10-21. Decisions 2 to 4 are checked again in W4, on real apps.

1. **Jaggery carries the app's traffic.** Every request, streaming answer, upload and WebSocket goes through the name holder. If it's away, the name fails over with it (seconds); the `machine.local:port` address keeps working throughout. Proposed: accept, and document the old address as the fallback.
2. **What `languard` adds must not break apps.** Port 80 sits behind it ([security-local.md](security-local.md)). Keep the parts that protect the house: the `Host` allow-list (app names are `.local`, so they pass) and the source-address check. But forwarded responses must keep the **app's own headers**: no `X-Frame-Options: DENY` added (some apps frame themselves), no stripping of the app's CORS or cookies. Cross-origin protection on the app's `POST`s: proposed **off for forwarded requests**, since apps carry their own (sign-in, CSRF tokens) and refusing their requests would break them. To confirm with real apps.
3. **Apps that build links from their own address.** n8n's webhook address and Open WebUI's `WEBUI_URL` are set by the owner and would still point at the old address. Proposed: send `X-Forwarded-Host`, `X-Forwarded-Proto` and `X-Forwarded-For`, and say in the docs which apps need their own setting changed; don't rewrite pages.
4. **Apps that refuse proxies.** Home Assistant answers 400 to a forwarded request unless the proxy's address is in its `trusted_proxies`. Other apps trust or rate-limit by client address and will see the name holder's address. Proposed: document per app (the catalog can carry a note), and treat "works without the app's own setting" as nice to have, not a requirement.
5. **Android.** `.local` names are ROADMAP's first risk on Android (E2); app names inherit it. Proposed: no special handling; A6 (tier-2 DNS) covers app names too if it is built.

## Not in this track

- **HTTPS.** Public authorities don't issue certificates for `.local`, so app names stay `http://`. That leaves passkeys (which need a secure page) and the plain-HTTP Wi-Fi exposure unsolved. Jaggery already runs a private CA for pairing; whether it should also issue certificates for the house's app names (and how phones would come to trust it) is a separate question for its own spec.
- **Sign-in.** No accounts, no single sign-on, no gate in front of apps: ROADMAP's "won't do" list.
- **Paths** (`house.local/chat`). Most apps break under a sub-path; a name per app is the only form that works for all of them.

## Slices

| # | Slice | Done when |
|---|---|---|
| W1 | **Names.** `jag apps name <app> <name>` / `--remove` on the machine running the app; names in the inventory; collisions and reserved names refused; `jag apps` and the page show them. **Done 2026-10-21**, with a test-house scenario. | Two machines can't give the same name; a removed name disappears from every page within a minute. |
| W2 | **Answering and forwarding.** The name holder answers app names over mDNS and forwards by `Host` to the app (HTTP and WebSockets), with `X-Forwarded-*`, keeping the app's headers (decision 2). **Done 2026-10-21**: a fake app (`scripts/fake-app.py`) opened by name through the holder, page, stream and WebSocket, on port 80 in CI. | The test house: a fake app on one machine, opened by its name through another (the holder), including a WebSocket echo and a streamed response; a `localhost`-only app isn't reachable. |
| W3 | **The page links to names**, falling back to `machine.local:port`. **Done 2026-10-21.** | A named app's tile opens its name; an unnamed one is unchanged. |
| W4 | **Real apps for a week.** Open WebUI, n8n and Home Assistant by name, from a phone and a laptop. | The per-app notes for decisions 3 and 4 are written from what happened, and the docs list them. |

## Answered (2026-10-21)

- **Names are given at the terminal**, one per app. The page may suggest names later; nothing is named without a person.
- **Cross-origin protection is off for forwarded requests** (decision 2): apps carry their own.
- **W doesn't wait for Android reports** (E2): app names work wherever `jaggery.local` does.
