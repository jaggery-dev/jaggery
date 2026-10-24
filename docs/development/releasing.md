# Releasing, and what to do about a bad release

For maintainers. The rules (versions, channels, the gates for `prod`) are in [releases.md](../../tech-specs/releases.md); this page is the checklist.

## A beta

Every merge to `beta` publishes `v<VERSION>-beta.<n>` from `release.yml`, signed. Nothing to do by hand. Machines on the beta channel install it one at a time.

## A prod release

1. Pick the candidate: a beta build that has run on every machine of a real house for at least 8 hours, with no rollback.
2. `sh scripts/promote.sh <beta version>`: checks what it can and lists the gates a person must confirm (soak, checked on hardware, upgrade and rollback from the current prod, release notes).
3. Tag the commit `v<VERSION>` (annotated, with the gates quoted). CI builds, signs and publishes it.
4. Move `VERSION` on `beta` to the next version.
5. Post the release notes in Discussions → Announcements and on jaggery.dev.

## A bad release

Something in a published release breaks machines, loses data, or is a security problem.

1. **Withdraw it:** `sh scripts/withdraw.sh <version> "<one line on why>"`. Machines skip a withdrawn release, and machines running it move to the newest release that wasn't withdrawn, even an older one. Nothing is deleted.
2. **Say so** in Discussions → Announcements: what's affected, what machines will do, whether anyone needs to act (for example a machine that can't update itself any more: `jag update --rollback`).
3. **Fix it on `beta` first,** as a normal pull request, and let it run on real machines for a few hours.
4. **Ship the fix to prod as a patch:** a branch from the prod tag, cherry-pick the fix, set `VERSION` to `X.Y.(Z+1)`, tag it. Prod machines with `auto` install it after a day's cool-down; those with `notify` see it in `jag status`, `jag doctor` and on the page.
5. **A security problem** also gets a GitHub security advisory, crediting the reporter unless they'd rather not be named.

## If the signing key leaks

Follow "If the main key leaks" in [releases.md](../../tech-specs/releases.md#signing-key-setup-once-by-a-maintainer): the spare key becomes the main one, and a new spare is made, in one release.
