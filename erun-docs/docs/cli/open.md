---
title: erun open
---

# `erun open`

Open a shell in the tenant environment. If the environment is not running, `open` deploys the runtime chart (a pod into the environment's namespace) and waits until it is ready before attaching a terminal.

## Synopsis

```
erun open [TENANT] [ENVIRONMENT] [flags]
```

Arguments resolve the same way as [`erun init`](/cli/init): from working directory, then defaults, then prompts.

## Flags

| Flag | Description |
|---|---|
| `--tenant <name>` | Open a specific tenant. |
| `--environment <name>` | Open a specific environment. |
| `--no-shell` | Don't attach a shell. Instead, print shell commands that switch `kubectl` context, namespace, and worktree locally. Useful for scripting and shell aliases. |
| `--no-alias-prompt` | When combined with `--no-shell`, skip the prompt that offers to create a local shell alias. |
| `--vscode` | Open the remote environment in VS Code (Remote-SSH) instead of a shell. |
| `--intellij` | Open the remote environment in IntelliJ IDEA Gateway instead of a shell. |
| `--version <version>` | Override the runtime chart and image version before opening. |
| `--runtime-image <repo>` | Override the runtime image repository before opening. |
| `--snapshot` / `--no-snapshot` | Turn snapshot deploys on/off for the **local** environment only. Ignored for non-local environments. |

## Examples

Open the default tenant/environment from inside a project:

```bash
erun open
```

Open a specific environment in VS Code:

```bash
erun open my-tenant rihards-dev --vscode
```

Print kubectl/namespace switching commands for shell scripting:

```bash
eval "$(erun open my-tenant local --no-shell)"
```

A common pattern is to alias each environment:

```bash
alias my-tenant-local='eval "$(erun open my-tenant local --no-shell)"'
```

## What `open` does

1. Resolves the effective tenant + environment.
2. Loads the env's `EnvConfig` (Kubernetes context, container registry, runtime version).
3. If a cloud context is linked, starts it and waits for the cluster API.
4. Runs `helm upgrade --install` for the runtime chart (and any opted-in component charts).
5. Waits for the runtime pod's SSH server to be reachable.
6. Establishes local port-forwards for SSH and MCP.
7. Attaches a terminal (default), prints shell commands (`--no-shell`), or launches an IDE (`--vscode`/`--intellij`).
