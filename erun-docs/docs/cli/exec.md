---
title: erun exec
---

# `erun exec`

Repository helpers that run from the project root. Two subcommands: `diff` (a structured git diff) and `raw` (run an arbitrary command).

## Synopsis

```
erun exec diff [flags]
erun exec raw COMMAND [ARG...] [flags]
```

## Subcommands

### `exec diff`

Shows the current git diff, including untracked files. Outputs raw diff text by default, or structured JSON with `--json` (files, hunks, lines, a directory tree, and the review base + commits).

| Flag | Description |
|---|---|
| `--scope` | `current` (default, worktree), `all` (since the review base), or `commit`. |
| `--selected-commit` | Oldest commit to include when `--scope=commit`. |
| `--json` | Emit the structured diff instead of raw text. |

### `exec raw`

Runs an arbitrary command from the project root. The command runs **directly, not through a shell** — wrap it in `sh -c "…"` if you need shell features. `--dry-run` traces the command (with secret-looking arguments redacted) without running it. Use `--` to pass flags through to the wrapped command rather than to `erun`.

## Examples

```bash
erun exec diff --scope all
erun exec raw go test ./...
erun exec raw --dry-run -- kubectl get pods --all-namespaces
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Not inside a git project. | Errors with "cannot find git project"; nothing runs. |
| `--selected-commit` without `--scope=commit`. | Errors before running git. |
| Wrapped command exits non-zero (`raw`). | Its exit code and stderr propagate; `erun` adds nothing. |
