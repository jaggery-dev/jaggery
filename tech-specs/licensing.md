# Licence, contributions and the name

**Decided 2026-10-18: Apache-2.0, contributions under the DCO with no CLA, and a trademark policy** (`NOTICE`, `TRADEMARKS.md`). The reasons follow.

## The short version

- **Apache-2.0** is what Jaggery's audience expects of something that finds devices on their network and holds keys. It works with app stores, NAS catalogues, distributions and the other projects a house runs, and it includes a patent grant from contributors.
- **Contributions come under the DCO** (`git commit -s`), not a CLA. Apache-2.0 is "inbound = outbound": a contribution comes under the same licence as the project, so a CLA would add friction and buy almost nothing.
- **The name is protected by a trademark policy,** because no open-source licence can stop someone shipping a confusing or malicious "Jaggery". The name will be registered before 1.0.

## The options considered

| | **Apache-2.0** | **MIT** | **MPL-2.0** | **AGPL-3.0** | Source-available (BSL, FSL, PolyForm) |
|---|---|---|---|---|---|
| Is it open source (OSI) | Yes | Yes | Yes | Yes | **No** |
| A company may ship it in a closed product | Yes | Yes | Yes, but must publish changes to Jaggery's own files | No: the whole product must be AGPL | Depends on the terms |
| Someone may run it as a paid hosted service, keeping changes private | Yes | Yes | Yes | No: users of the service get the source | Usually forbidden |
| Patent grant from contributors | **Yes**, and it ends for anyone who sues | No | Yes | Yes | Varies |
| Fine in the Mac App Store and iOS App Store | Yes | Yes | Yes | **No** in practice (the store's terms add restrictions the GPL family forbids; VLC was pulled for this) | Varies |
| Fine with Jaggery's dependencies (all MIT, BSD, Apache-2.0) | Yes | Yes | Yes | Yes | Yes |
| Accepted by NAS app catalogues, distros, Homebrew | Yes | Yes | Yes | Yes | Often not |
| What homelab readers think | Normal | Normal | Fine | Respected; some companies ban AGPL internally | Hostile: "not open source" threads |
| Similar projects | Home Assistant, Ollama (MIT), Tailscale client (BSD), CasaOS | Ghostty, Ollama | Syncthing | Immich, Grafana (since 2021), Nextcloud | Umbrel (PolyForm), HashiCorp tools after 2023 |

**Source-available licences are ruled out:** the people Jaggery is for reject "open but not open source" for software like this, and rightly.

**MIT** is simpler but lacks the patent grant and the explicit rule that contributions come under the same licence.

**MPL-2.0** would require changes to Jaggery's own files to be shared while allowing it inside bigger closed products. A real option, but weaker than its neighbours at what each does best.

**AGPL-3.0** would stop a company shipping Jaggery inside a closed product without sharing its changes. Jaggery is software people run in their own house, with little to host, so the AGPL's network clause guards against a business that's hard to picture, while it would rule out app-store builds and put off some companies and packagers.

## Contributions: DCO, not CLA

| | **DCO** (Developer Certificate of Origin) | **CLA** (Contributor Licence Agreement) |
|---|---|---|
| What the contributor does | Adds `Signed-off-by:` to each commit (`git commit -s`), certifying they have the right to submit it | Signs an agreement once, usually through a bot |
| What the project gets | The code under the project's licence | A broad licence or copyright assignment: the right to relicense |
| Friction | Almost none | Real: some people and companies won't sign one |
| Trust | High: used by Linux, Docker/Moby, GitLab | Lower, since several companies used CLAs to close their projects |

Contributors keep the copyright in their changes. A GitHub `noreply` address is fine in the sign-off.

## The name

Apache-2.0 deliberately grants no trademark rights (section 6). [TRADEMARKS.md](../TRADEMARKS.md) says how the name and logo may be used: freely to refer to the project and to package it unmodified; a changed version needs another name; no "official" claims. Separately, **the signing keys protect the update channel:** only builds signed with Jaggery's release key install as updates, so a fork can't push code to Jaggery houses even under the same name.

## Copyright

- `NOTICE` says "Copyright 2026 The Jaggery Authors", the convention Go and Chromium use: accurate, and git history and sign-offs record who wrote what. Apache-2.0 asks redistributors to keep `NOTICE`.
- **Code written with AI tools:** much of Jaggery was written with AI coding tools under a person's direction, and every change is reviewed and answered for by a person (see "AI-assisted contributions" in [CONTRIBUTING.md](../CONTRIBUTING.md)). Copyright in machine-written code differs by country; under a permissive licence that matters little.
- No SPDX headers in each file: the root `LICENSE` is enough for a Go project.
