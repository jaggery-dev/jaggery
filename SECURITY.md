# Security

Jaggery runs on every machine in a home, holds keys that let machines trust each other, and can replace its own binary. We take reports seriously and will answer within two working days.

## Reporting a problem

Please **don't open a public issue**. Instead, use [GitHub's private vulnerability reporting](https://github.com/jaggery-dev/jaggery/security/advisories/new), or email **hello@jaggery.dev** with "security" in the subject.

Tell us what you found, how to reproduce it, and what an attacker could do with it. We'll confirm, agree a fix and a date with you, and credit you in the release notes unless you'd rather not be named.

## In scope

- A machine becoming trusted without a valid code or an approval
- Reading or changing another machine's state, keys or shared files without being trusted
- Escaping a share (reading outside it, dotfiles, `*.key`, `*.pem`), or writing to a read-only share
- Installing a build that didn't come from a release, or bypassing rollback
- Sending to a hosted provider, or spending, outside `providers.json`'s allow-list and cap, or without an outbound log entry
- The agent exposing something to the internet

## Known and documented, not vulnerabilities

These are listed in [docs/security.md](docs/security.md#what-is-not-protected-yet) and are being worked on:

- `ai.local`, the MCP server and the page answer anyone on the home network (per-person keys are planned).
- Local names are plain HTTP.
- Releases are signed with a key held by CI, not yet keyless.

## Supported versions

The latest `prod` release, and the latest `beta`. Fixes land on `beta` first and reach `prod` as a patch release.
