---
title: erun open
---

# `erun open`

Open a shell in the tenant environment. If the environment is not running, `open` deploys the runtime chart (a pod into the environment's namespace) and waits until it is ready before attaching a terminal.

`open` is how the **Operator joins an environment**. The env has two endpoints on the same pod — SSH and [MCP](/mcp/overview) — both accepting any client. IDEs and the Claude Code / Codex desktop apps attach over SSH; Agents typically use MCP for structured calls and SSH for shell work. Either way, same files, same shell, same audit trail.

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
| `--vscode` | Open the remote environment in VS Code (Remote-SSH) instead of a shell. |
| `--intellij` | Open the remote environment in IntelliJ IDEA Gateway instead of a shell. |

Advanced flags (`--no-alias-prompt`, `--version`, `--runtime-image`, `--snapshot`/`--no-snapshot`) and the full open lifecycle algorithm are on [Agent reference · CLI flag spec · `erun open`](/agent-reference/cli-flags#erun-open).

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

`open` resolves the env, brings up its cloud context if linked, and ensures the runtime is deployed — for a build-enabled (agent) env that includes building and pushing the runtime image first. It then helm-installs (or upgrades) the chart, persists the resolved version/snapshot choice to the env config, waits for SSH readiness, port-forwards SSH + MCP, and attaches a terminal or IDE. The full numbered algorithm — including the cluster-API readiness loop, the SSH banner probe, and the port-forward state-file format — is on [Agent reference · `erun open` lifecycle](/agent-reference/cli-flags#erun-open-lifecycle-algorithm).

## Error behaviour

Common failures: tenant/env not configured (suggests `erun init`), kubeconfig context missing, cluster unreachable, helm upgrade failed, SSH readiness timeout. `erun doctor` from another shell diagnoses most cases. Full code + exit-code table: [Agent reference · CLI flag spec · `erun open` error codes](/agent-reference/cli-flags#erun-open).
