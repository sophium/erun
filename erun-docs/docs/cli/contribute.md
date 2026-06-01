---
title: erun contribute
---

# `erun contribute`

Contribute-mode helpers for working on ERun itself from inside an environment.

## Synopsis

```
erun contribute clone [flags]
```

## Subcommands

### `contribute clone`

Clones the ERun source repository to `$HOME/git/erun` (over HTTPS, on its default branch) and installs a small `erun` shim on `PATH` so that, inside a contribute environment, every `erun` invocation runs the local checkout's build instead of the installed binary. It is idempotent: if the clone already exists it verifies the origin and skips re-cloning, but always re-installs the shim.

| Flag | Description |
|---|---|
| `--dry-run` | Print the clone + shim actions without performing them. |

## Examples

```bash
erun contribute clone --dry-run
erun contribute clone
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| `$HOME/git/erun` exists but its origin isn't the ERun repo. | Errors rather than touching an unrelated clone. |
| Clone fails (network/auth). | Surfaces the git error; the shim is not installed. |
