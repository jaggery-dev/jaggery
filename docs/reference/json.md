# JSON output

Commands that only show something take `--json`, for scripts and agents. Without it, output is for people and its wording can change.

| Command | `data` holds |
|---|---|
| `jag status --json` | this machine's name, id, fingerprint, state directory, `inventory`, `update`, `interfaces`, `ignored_interfaces` and `peers` |
| `jag peers --json` | `peers`: id, name, trust, whether its key is verified, its fingerprint, addresses, port, `vouched_by`, `same_name` (another machine, maybe this one, uses its name), first and last seen |
| `jag map --json` | `machines` (the house snapshot the agent writes), `name_holder` (the machine answering for jaggery.local and ai.local), `name_claims` (devices outside the house heard answering for them), `updated_at` |
| `jag models --json` | `approval_enforced`, and `models`: each copy of a model with its machine, runtime, format, digest, whether it is loaded and whether it is approved |
| `jag ai --json` | `paused`, and `pause` while it holds: `since`, and `until` unless it lasts until `jag ai resume` |
| `jag doctor --json` | `checks`: each check's `name`, `state` (`pass`, `warn` or `fail`), `detail`, and the `fix` for each check that didn't pass |
| `jag activity [day] --json` | `day` and `entries`, the house's AI requests |
| `jag outbound [day] --json` | `day`, `entries` (what left the house for hosted models) and `spending` per provider |
| `jag outbound show <id> --json` | the payload sent and the reply |
| `jag share --json` | `shares`: name, machine, writable, online; `path` only for this machine's own shares |
| `jag files log [day] --json` | `day` and `entries`, the calls on shares |
| `jag files versions <share> <path> --json` | `versions`: each old copy's name, time and size |
| `jag update --check --json` | `running`, `channel`, `latest` (version, commit, build time), where it was found (`from`) and whether it is `available` |

Commands that change something (`approve`, `share add`, `update` without `--check` and the rest) refuse `--json` with a usage error: they stay at a terminal, run by a person.

## The envelope

Every command prints one JSON document on stdout, and nothing else there; warnings and progress go to stderr.

```json
{"version": 1, "ok": true, "data": {"peers": []}}
```

```json
{
  "version": 1,
  "ok": false,
  "error": {
    "code": "no_identity",
    "message": "no identity on this machine yet: run 'jag up' first",
    "fix": "jag up"
  }
}
```

- `version` is the envelope's version. Adding a field doesn't change it; renaming or removing a field, or changing what one means, does.
- `ok` is true when the command did what was asked.
- `error.code` is stable: branch on it. `error.message` is for people and can change. `error.fix`, when there is one, is the command that fixes it.
- `jag doctor` with a failing check sets `ok` to false, `error.code` to `checks_failed`, and still gives every check in `data`.

| `error.code` | Means |
|---|---|
| `usage` | the command line was wrong, or the command doesn't take `--json` |
| `no_identity` | this machine hasn't run `jag up` yet |
| `not_running` | the agent isn't running on this machine |
| `no_snapshot` | the agent hasn't written the house snapshot yet |
| `not_found` | no share, file version or payload by that name |
| `checks_failed` | `jag doctor`: at least one check failed |
| `failed` | anything else; the message says why |

The exit code is the same as without `--json`: see "Exit codes" in [cli.md](cli.md).
