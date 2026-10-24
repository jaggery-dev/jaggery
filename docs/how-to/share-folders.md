# Lend a folder to models and agents

Shares let models and agents read, and if you allow it write, folders anywhere in the house through one MCP server at `http://ai.local/mcp`.

## Add a share

On the machine that has the folder:

```sh
jag share add ~/Documents/recipes                   # read-only
jag share add ~/Projects/notes --name notes --writable
jag share                                           # every share in the house
```

The rest of the house sees the share's name, never its path. The agent can serve only what its own user can read.

For a NAS: mount it on a machine and share the mount point, or run `jag` on the NAS.

## Connect an agent

Point the agent's MCP settings at `http://ai.local/mcp` (streamable HTTP). It gets six tools: `list_shares`, `list_dir`, `read_file`, `search`, and for writable shares `write_file` and `delete_file`. Text, images and other files are returned by type; large files are read in chunks.

## What's protected

- Read-only unless added with `--writable`.
- Dotfiles and dot-folders (`.git`, `.ssh`, `.env`) and `*.key`, `*.pem` are never served.
- Every write or delete keeps the old copy for 30 days, in `.jaggery-versions` inside the share.
- Every call is logged on the machine that holds the share.

## See what happened, and undo it

```sh
jag files log                              # today's reads and changes
jag files log 2026-10-15 --share notes
jag files versions notes todo.md           # old copies, newest first
jag files restore notes todo.md            # put the newest old copy back
```

A restore keeps the replaced file as a version too, so it can be undone.

## A word on what agents read

A file in a share can contain text written to steer an AI ("ignore your instructions and…"), and an agent that reads it may follow it. Jaggery doesn't filter for that. It's one reason shares are read-only by default: share a folder `--writable` only if you'd let the agent change it unsupervised, keep secrets out of shares, and check `jag files log` now and then. More in [security](../security.md#what-is-not-protected-yet).

## Stop sharing

```sh
jag share remove notes
```

The folder itself is untouched.

**Not yet:** per-person control of shares. Until per-person keys arrive, anyone on your home network can reach the shares through `ai.local/mcp`. Share accordingly.
