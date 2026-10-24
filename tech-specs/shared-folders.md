# Jaggery: shared folders for models and agents (track S)

**Status: agreed 2026-10-14; S1 to S3 built, S4 (real use) under way, S5 after per-person keys.** Part of [the plan](plan.md). It replaced an earlier design that labelled data for a content gate, which no longer exists ([decisions.md](decisions.md#agents-and-content-cmdjagmcpgo-internalshares-internalhosted)).

**Goal:** the person picks folders on any machine in the house, including a NAS, and the house's models and agents can read and write files in them through one place, `ai.local/mcp`. Every access is logged, and later controlled per person and per folder.

**Done when:** an agent app given only `http://ai.local/mcp` reads a PDF from a folder on one machine and writes a summary into a writable folder on another; both show in `jag files log` with the app, the device, the path and the size; and the overwritten summary can be brought back with `jag files restore`.

## How it works
```
agent app ──MCP over HTTP──▶ ai.local/mcp  (any machine)
                                 │  logs the call: device, app, tool, share, path, bytes
                                 ├─ share on this machine ─▶ served here
                                 └─ share on a peer ─mTLS─▶ the peer serves it and logs it too
```

- **A share is a folder on the machine that holds it.** Files stay where they are. Nothing is copied or indexed centrally, and a read goes to the machine that holds the file over the mTLS links the house already has.
- **A NAS joins in one of two ways:** mount it on a machine (SMB or NFS) and share the mount point, or run `jag` on the NAS itself (Synology, TrueNAS and Unraid run Linux on amd64 or arm64). Jaggery doesn't speak SMB or NFS itself.
- **Shares are added only at the terminal of the machine that holds them**, like model approval. One compromised machine can't publish another machine's folders, and the dashboard stays read-only while it is open to the LAN (until K1).
- **Jaggery runs the MCP server itself,** rather than putting a gateway in front of a third-party filesystem server. Every read and write is executed by Jaggery, so logging and, later, control sit where the access happens. Files are the most sensitive tools an agent has, so they are done first-party now; the general gateway for other MCP servers (K3) reuses the same pattern after per-app keys. The same `/mcp` later carries H3's read-only house tools (machines, models, health).
- **Built on** the official MCP Go SDK (`modelcontextprotocol/go-sdk`, Apache-2.0; checked in [agent-platform-eval.md](agent-platform-eval.md)) over streamable HTTP, and Go's `os.Root`, which refuses `..` and symlinks that lead outside the share at the OS level.

## Every file type
Jaggery moves bytes; it doesn't interpret them. Understanding a PDF or a spreadsheet is the job of the model, or of tools the app gives it such as a code sandbox. An MCP tool result is a list of content blocks, so `read_file` picks the block by MIME type:

| File | Returned as | What the model gets |
|---|---|---|
| Text: code, CSV, JSON, Markdown, plain text | a text block | the text |
| Images (PNG, JPEG, WebP, GIF) | an image block (base64 and its MIME type) | a picture, on models that take images |
| Anything else (PDF, docx, xlsx, zip, audio, …) | an embedded resource (the bytes and their MIME type) | whatever the app does with it: some apps pass PDFs to models that read them, others hand files to a code sandbox |

Base64 adds a third to the size, and a large file would fill a model's context, so reads take an `offset` and a `length`, have a maximum per call (1 MiB to start), and always report the file's total size. An agent reads in chunks or only what it needs.

## Writes
A share is read-only unless it was added with `--writable`. This narrows the rule that agents are read-only ([decisions.md](decisions.md#agents-and-content-cmdjagmcpgo-internalshares-internalhosted)): that rule covers house actions, and for files the risk is handled by making every change undoable rather than forbidding it.

- **Every overwrite or delete keeps the old version** in a hidden `.jaggery-versions` folder inside the share for 30 days. `jag files restore <share> <path> [version]` brings it back. A document an agent reads can tell it to overwrite or delete files (prompt injection); with versions the damage can always be undone, and keeping versions needs no judgement of content.
- **Every write is logged** like a read, with a hash of the old and new contents.
- **One whole file at a time:** no partial writes, renames, moves, or folder deletes to start with. Up to 8 MiB per write.
- Until K1, anyone on the LAN who can reach `/mcp` can write to a writable share. That is accepted for now because shares are read-only by default and every write can be undone.

## What it can't do
Jaggery logs that an agent read a file, and the outbound log shows what went to a hosted provider. It can't stop a file's contents reaching a provider once an agent running on that provider's model has read it; that would need content inspection, which was tried and dropped. The controls are which folders are shared at all, a complete record of every access, and (S5) who may read or write each share. A folder that must never leave the house shouldn't be shared until S5.

## Slices
| # | Slice | Done when |
|---|---|---|
| S1 | **Shares.** `jag share add <path> --name <name> [--writable]`, `jag share remove`, `jag share list`, on the machine that holds the folder, kept in `shares.json` in the state directory. Opened through `os.Root`. Built-in excludes: dotfiles and dot-folders (including `.git` and `.ssh`), `*.key`, `*.pem`. Share names (not paths or contents) travel in each machine's inventory; the dashboard lists them with their machine and whether they're writable. | A share added on one machine shows on every machine's dashboard; a symlink leading outside the share and an excluded file are refused. |
| S2 | **`/mcp` on every machine.** Tools: `list_shares`; `list_dir(share, path)` with names, sizes, MIME types and modified times; `read_file(share, path, offset, length)` returning a text, image or resource block as above, plus the total size; `search(share, query)` on file names and a bounded search through text files, run on the machine holding them; `write_file(share, path, content, encoding?)` (a whole file, text or base64) and `delete_file(share, path)` on writable shares only, keeping the old version. A share on a peer is reached over mTLS. | An agent app pointed at `http://ai.local/mcp` lists, reads and searches a share on another machine, writes to a writable share, and is refused on a read-only one. |
| S3 | **Access log.** One line per call: time, device, app (the MCP client's name from its `initialize` handshake), tool, share, path, bytes, and hashes for writes. Written on the machine that served the call and on the one that took it, in a log of its own beside the activity log, and shown in full by `jag files log`. The dashboard shows counts per share and app, not paths (a path like `medical/scan.pdf` says something on its own), until K1. `jag files restore` from the versions folder. | Every call in S2's test is in the log on both machines; a deleted file is restored. |
| S4 | **Real use for a week.** A real house points its agent app at real folders and uses it day to day; then the log is reviewed. | The person says the log answers "what did the agents read and change?", and the control that matters most is known. |
| S5 | **Control, after K1 (per-person and per-app keys).** Rules per key and per share: read, write or none, with path globs. | A key without access to a share can't read it, and the refusal is logged. |

S1 to S3 ship as one beta release. S4 is the real-use check from [decisions.md](decisions.md#how-we-test): try the simplest version on real use before adding control. K1 comes straight after S4, since until then the log names a device, not a person.

## Later
- Search by meaning over shares (embeddings on the machine holding the data; D2).
- Renames, moves and folder operations, once S4 shows they're wanted.
- Connectors for apps that hold data (Nextcloud, Paperless-ngx, Immich, IMAP), each exposed as a share.

## Progress
- **S1 code, 2026-10-14** (`internal/shares`, `cmd/jag/share.go`): `jag share add|remove|list`, `shares.json` (`0600`) in the state directory, names and modes in the inventory (never paths), a "Shared folders" list on the dashboard. Add refuses the filesystem root, the whole home folder, Jaggery's state folder, overlapping shares and duplicate names. Paths from agents are cleaned under the share, dot-paths and `*.key`/`*.pem` are refused, and files open through `os.Root`; a test shows a symlink leading outside the share is refused and one inside it is followed. Hardware check (a share on one machine shown on the others) pending a release.
- **S2 code, 2026-10-15** (`internal/shares/ops.go`, `cmd/jag/files.go`, `cmd/jag/mcp.go`): `/mcp` on every machine's LAN port with the official MCP Go SDK (v1.8.0; it and its linked dependencies are MIT, BSD or Apache-2.0), six tools as above. A share on another machine is reached over mTLS at `POST /v1/files` on the secured listener, and the holder checks the request against its own `shares.json`. Shares with the same name on two machines are told apart as `docs@nas`. Reads up to 1 MiB, cut on character boundaries for text; writes go to a temporary file, the old version is kept (a hard link where possible, else a move) and pruned after 30 days. Browsers are refused (`http.NewCrossOriginProtection`), so a web page can't drive the tools. One journal line per call on the machine that served it (S3 builds the log on this). Test: an MCP client lists, reads text, an image and a PDF, is refused `.env` and a read-only write, writes twice to a share on a second machine over mTLS (the first version kept), and searches. Hardware check pending a release.
- **S3 code, 2026-10-15** (`internal/shares/log.go`, `cmd/jag/filescmd.go`): an access log per machine in `files/<day>.jsonl` (`0600`, kept 30 days): time, app (from the MCP handshake), address, `via` (the machine that passed the call on) or `served by` (the machine it was passed to), operation, share, path or query, bytes or matches, and for writes, deletes and restores the old and new SHA-256 and where the old version went; refusals with their reason. `jag files log [day] [--share]`, `jag files versions <share> <path>`, `jag files restore <share> <path> [version]` (on the holder; the file it replaces becomes a version too, so a restore can be undone). Each share's inventory entry carries today's counts and app names (no paths), shown on the dashboard. Separate from the model activity log: a file call isn't a model request, and keeping the two apart keeps both simple.

## S4: a week of real use
**Set-up:** on the machines that hold them, share the folders agents should use (`jag share add <folder>`; `--writable` only where agents should change files). Point the agent app at `http://ai.local/mcp`, or at one machine's `:7373/mcp` from inside a container. Then use it as normal for a week.

**What to try:** questions that need a document (a PDF, a spreadsheet, a photo); a summary written into a writable share; a search across a share; one deliberate mistake that gets undone with `jag files restore`.

**Review at the end** (from `jag files log` on each holder):
1. Can the person tell, from the log alone, what each agent read and changed, and why?
2. Did an agent read anything it had no reason to? Which folders would they now share differently?
3. Which file types did the models fail on (likely scanned PDFs and office files), and is that the app's job or ours?
4. Were the limits right: 1 MiB reads, 8 MiB writes, 100 search matches?
5. What control matters most for S5: per-app, per-person, per-folder, or approval of writes?

Findings go here as S4's record.
