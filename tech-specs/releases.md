# Versions, channels and releases

**Status: agreed 2026-10-07** (SemVer, two channels, promotion on quality gates; 1.0 at the public launch; ROADMAP milestones named, not numbered). The public repository starts at `v0.6.0`; earlier releases were made before the source was published.

## Versions: SemVer 2.0

- A `VERSION` file in the repo holds the **next** release, e.g. `0.5.0`.
- Every merge to `beta` publishes **`v0.5.0-beta.N`**, N counting up and never reused.
- Promoting to `prod` tags the same commit **`v0.5.0`**; CI refuses a prod tag that doesn't match `VERSION` on that commit. SemVer ranks it above every `0.5.0-beta.N`. Beta machines don't switch to it: they already run that exact code, and the next beta (`0.6.0-beta.N`) outranks both. `VERSION` on `beta` then moves to the next version (`0.6.0`).
- A build made by hand is **`0.0.0-dev+<commit>`** (the commit from Go's build info, `.dirty` if uncommitted changes), older than any release, and never replaced unasked.
- The updater orders releases by SemVer; build time only breaks ties.

What each part promises:

| Part | Means | Updates |
|---|---|---|
| PATCH (`0.5.1`) | fixes only | automatic, after a short cool-down |
| MINOR (`0.6.0`) | features; still reads the previous minor's state files and talks to peers one minor behind (N-1) | `notify` before 1.0; automatic after a longer cool-down from 1.0 |
| MAJOR (`2.0.0`) | breaking change | **only with the person's consent**, never automatic |

## Compatibility contracts
Versioned apart from the release, because a house always has machines on different versions during a rollout:
1. **House protocol:** the mTLS APIs between agents. Each agent announces the protocol versions it speaks; an incompatible peer is flagged on the map, not left to fail silently. Agents interoperate with peers one minor release behind.
2. **State files:** each carries a schema version. Migrations only go forward and keep a backup, so the automatic one-step rollback still works.
3. **Public surface:** `ai.local`, the CLI, `/v1/house`. Breaking them needs a major version from 1.0 on.

## Channels
- **`beta`:** every merge. The maintainers' houses follow it automatically, and so can anyone who opts in (`jag update --channel beta`). Beta is where builds prove themselves.
- **`prod`:** promoted releases. Testers and users default to it, with `notify` until 1.0.

## Merging into beta
A contributor's PR merges into `beta` once GitHub's CI passes and a maintainer has reviewed it. A maintainer's own PR may merge once `scripts/ci-local.sh` passes on its branch: the same checks as `.github/workflows/ci.yml` (gofmt, vet and race tests with the Go version in `go.mod`, the personal-material check, the smoke test, every cross-build), run on the machine doing the merge. GitHub's CI still runs; if it later fails where the local run passed, fix it on `beta` straight away.

**Releases come from `release.yml`:** a signed release must come from CI. CI runs once per pull request (documentation-only changes skip it).

## Workflow safety
Pull requests from forks and the text of issues and PRs are untrusted input (checked 2026-10-23, C4):
- **CI runs on `pull_request`, never `pull_request_target`,** with `contents: read` and checkouts that don't keep the token. Fork PRs get no secrets, so the personal-material check runs only its built-in patterns there; maintainers run the full list with `scripts/ci-local.sh` before merging. A first-time contributor's run waits for a maintainer's approval.
- **The signing key is only in `release.yml`'s `release` environment,** which only `beta`, `prod` and `v*` tags can read; pull requests can't.
- **No workflow runs an AI agent** on issue or PR text.
- **Actions are pinned to a commit,** with the version in a comment. Update them by hand, deliberately.

## Signing key setup (once, by a maintainer)
**Done 2026-10-17:** the `release` environment, `JAGGERY_SIGNING_KEY` in it, and the spare in a maintainer's password manager; the public keys are `internal/update/keys/main.pem` and `spare.pem`.

**Rotated 2026-10-24,** when the repository moved to its public home: GitHub secrets can't be copied between repositories, and the first main key had only ever existed as a secret. As planned below, the spare became the main key (it signed `v0.6.0`), a new spare was made and kept offline, and the old main key is no longer trusted from `0.7.0-beta` builds on.

Releases are signed in CI with an ed25519 key. The private keys never touch a disk or pass through anyone else's session. Run these on a machine with OpenSSL 1.1.1 or newer (macOS's LibreSSL may lack ed25519) and the `gh` CLI.

1. **The protected environment.** Repo **Settings → Environments → New environment** `release`. Under "Deployment branches and tags" pick "Selected branches and tags" and add the branches `beta` and `prod` and the tag pattern `v*`. No required reviewers, so betas still publish on their own. Only runs from those refs can read the key.
2. **The main key, straight into the secret:**
   ```sh
   key=$(openssl genpkey -algorithm ed25519)
   printf '%s\n' "$key" | gh secret set JAGGERY_SIGNING_KEY --env release -R jaggery-dev/jaggery
   printf '%s\n' "$key" | openssl pkey -pubout    # the main public key: goes into jag
   unset key
   ```
3. **A spare key, for rotation.** Its public key is built into `jag` too, so if the main key leaks, releases can switch to the spare without stranding machines already in the field. Its private key lives only in a maintainer's password manager, never in GitHub or on disk:
   ```sh
   spare=$(openssl genpkey -algorithm ed25519)
   printf '%s\n' "$spare" | openssl pkey -pubout  # the spare public key: goes into jag
   printf '%s\n' "$spare"                         # into the password manager
   unset spare
   clear
   ```
4. **Commit both public keys** as `internal/update/keys/main.pem` and `spare.pem`. `release.yml` signs every release with the main key, and machines refuse unsigned or wrongly signed updates.

**If the main key leaks:** put the spare's private key in `JAGGERY_SIGNING_KEY`, make a new spare (step 3), and in one PR replace `main.pem` with the old spare's public key and `spare.pem` with the new one. Machines already in the field trust the old spare, so they install that build, and from it on the leaked key is no longer trusted.

## Promotion: beta → prod
**Pick a candidate, don't cut a fresh one.** The candidate is a beta build's commit that has already run in the house. Prod is that same commit, rebuilt only to stamp the release version (reproducible: `-trimpath`, fixed flags).

**Gates.** All must hold, and are quoted when promoting:
1. **Soak:** the candidate commit has run on every house machine for at least **8 hours**, with no rollback and no machine marking it bad. 
2. **Checked on hardware:** every slice in the release has met its done-when on real machines.
3. **Upgrade and rollback:** a machine on the current prod updates to the candidate, and rolls back to the old prod with its state intact.
4. **Release notes** written, from the PR titles since the last prod release.
5. **Milestones are claims, not triggers:** a release may *claim* a ROADMAP milestone only if that milestone's done-when is met on hardware.

**Mechanics:** fast-forward `prod` to the candidate, tag `vX.Y.Z` (annotated), CI publishes the full release with the notes, and `VERSION` on `beta` moves on.

**Cadence:** whenever the gates pass, roughly every 1–2 weeks during the alpha. Milestones don't hold releases back.

## Fixes to prod
**Beta first, always.** The fix merges to `beta`; then a branch from the prod tag cherry-picks it, and `vX.Y.(Z+1)` ships after a short soak (hours). Beta is never behind prod, and nothing reaches prod without having run on beta.

## Rollout across houses
There is no central server, so each house paces itself:
- **Cool-down:** a prod agent installs a release only once it has been out for a while (about 1 day for a patch, 3 days for a minor after 1.0). Early adopters' houses find problems before most houses update.
- **Withdrawal:** a release found bad is marked withdrawn. Agents don't install it, and those that have it roll back, as a house already does with a bad beta build.
- Inside a house, the existing stagger applies: the name holder first, the rest after it has proven the build.

## 1.0
**The public launch is `v1.0.0`** (decided 2026-10-07). The alpha runs `0.x`; at 1.0 the compatibility promises above start to bind, because strangers on automatic updates need them. Until then, breaking changes may ship in a minor release, with a note.

## Milestones
ROADMAP's milestones have names (Map, Names, AI router, App stores, Launch), so numbers mean only releases (decided 2026-10-07). A release names the milestones it claims in its notes.
