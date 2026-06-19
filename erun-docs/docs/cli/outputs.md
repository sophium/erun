---
title: erun outputs
---

# `erun outputs`

List and download the files an agent (Claude/Codex) produced inside an environment's runtime pod. This is the inverse of pasting a file *into* the pod from the desktop terminal — `erun outputs` pulls deliverables *out*.

Agents and skills write deliverables (reports, generated artifacts, build outputs, log bundles) to a canonical **outputs directory** in the pod — `$ERUN_OUTPUTS_DIR`, which defaults to `/home/erun/.erun/outputs`. It lives on the environment's home volume, so it survives pod restarts. `erun outputs list` shows what's there; `erun outputs download` brings an entry onto your machine. Files that belong in the repository still go to git as usual; the outputs directory is for intermediate and diagnostic deliverables.

Both subcommands target the current scope by default; pass `--tenant`/`--environment` to target another, and `--path` to read a different directory under the pod's home.

## `erun outputs list`

```
erun outputs list [--tenant <t>] [--environment <e>] [--path <dir>] [--limit <n>] [flags]
```

Lists the outputs directory newest-first as a table of `name  size  modified  type`. Read-only. With the global `--output json` it emits the listing as a structured result instead.

```bash
erun outputs list                          # newest-first table for the current env
erun outputs list --environment prod --limit 20
erun outputs list --output json            # structured result for scripts/agents
```

## `erun outputs download`

```
erun outputs download <name> [--tenant <t>] [--environment <e>] [--path <dir>] [--dest <local-path>] [--force] [flags]
```

Downloads one entry. A file lands as-is; a folder lands as a `<name>.tar.gz` archive. By default it writes into the current directory; `--dest` chooses a different file or directory, and `--force` overwrites an existing local file. The destination flag is `--dest` (not `--output`, which is the global text/JSON mode). After a successful download it prints the local path, size, and SHA-256.

```bash
erun outputs download report.pdf           # into the current directory
erun outputs download results --dest ./out # a folder, saved as results.tar.gz under ./out
```

## Dry run

Both subcommands support `--dry-run`: it resolves the pod, traces the exact `kubectl exec` that would run and the listing/transfer script, and (for `download`) the local destination — without listing or transferring anything.

## Size limit

A single download is capped at 100 MB (the transfer buffers the whole payload in memory). A file over the limit fails with a clear error before any transfer.

## From the desktop and over MCP

The desktop app surfaces the same outputs through an **Agent outputs** dialog (the Outputs button on an environment row): it lists the deliverables newest-first and saves a chosen one through a native Save dialog. Agents reach the same files over MCP with the `outputs_list` and `outputs_download` tools — see [MCP overview](/mcp/overview).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any pod contact with the resolution failure. |
| Outputs directory doesn't exist. | `list` prints an empty listing (not an error); `download` errors with `not found`. |
| Entry not found. | `download` errors with `not found: <path>`. |
| Entry exceeds the 100 MB limit. | `download` errors before transferring. |
| Local destination already exists. | `download` errors unless `--force` is passed. |
| Invalid entry name (`.`/`..`/empty). | `download` errors before any pod contact; a name with directory components is reduced to its base segment so it can't escape the outputs directory. |
