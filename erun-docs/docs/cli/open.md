---
title: erun open
---

# `erun open`

Open a shell in the tenant environment. `open` is a **pure primitive**: it checks the runtime is **already deployed**, attaches a terminal to it, and best-effort port-forwards SSH + [MCP](/mcp/overview) + API for local tooling on the way in. It does not build, push, mint a version, or deploy. Bringing the runtime up is the caller's job — run [`erun deploy`](/cli/deploy) first, or pass `--deploy` to deploy before opening (the operator-convenience shortcut: builds-here envs build → push → deploy, runtime envs install the current version). The desktop app composes [`build`](/cli/build) → [`push`](/cli/push) → [`deploy`](/cli/deploy) itself and opens the pure shell.

`open` is also what **starts a stopped environment**. If the environment was stopped with [`erun stop`](/cli/stop), `open` scales its runtime back up and waits for it before it forwards any port — a port-forward cannot attach to an environment with no pod. It says so while it waits, because a cold start takes a moment. An environment that is already running gets no extra work.

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
| `--deploy` | Deploy the runtime before opening (operator convenience: builds-here envs build → push → deploy, runtime envs install the current version). |
| `--vscode` | Open the remote environment in VS Code (Remote-SSH) instead of a shell. |
| `--intellij` | Open the remote environment in IntelliJ IDEA Gateway instead of a shell. |

Advanced flags (`--no-alias-prompt`, `--version`, `--runtime-image`) and the full open lifecycle algorithm are on [Agent reference · CLI flag spec · `erun open`](/agent-reference/cli-flags#erun-open). `open` is a pure primitive — it just opens a shell to the already-deployed environment; it does not build, push, or deploy on its own. `--deploy` is the operator-convenience shortcut that deploys first; programmatic callers (the desktop app) instead orchestrate [`build`](/cli/build) → [`push`](/cli/push) → [`deploy`](/cli/deploy) themselves and open the pure shell (see [Command primitives](/concepts/command-primitives)).

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

`open` resolves the env, brings up its cloud context if linked, then — without deploying — checks the runtime is deployed, starts it if it was stopped and waits for it to become available, refreshes the env's [host AWS credentials](/deployment/cloud-setup#host-credentials) if it carries an AWS alias, waits for SSH readiness, best-effort port-forwards SSH + MCP + API for local tooling, and attaches a terminal or IDE. It never builds, pushes, or rolls out a chart. If the runtime is not yet deployed, `open` says so up front and tells you to run [`erun deploy`](/cli/deploy) first or pass `--deploy`; with `--deploy`, `open` deploys before opening (builds-here envs build → push → deploy, runtime envs install the current version). A port-forward that can't bind is a warning, not a failure — the shell runs inside the pod and doesn't depend on it. The full numbered algorithm — including the cluster-API readiness loop, the SSH banner probe, and the port-forward state-file format — is on [Agent reference · `erun open` lifecycle](/agent-reference/cli-flags#erun-open-lifecycle-algorithm).

## Error behaviour

Common failures: tenant/env not configured (suggests `erun init`), kubeconfig context missing, cluster unreachable, runtime not deployed (`open` detects this up front and tells you to run `erun deploy` or `erun open --deploy`), SSH readiness timeout. A port-forward that can't bind does not fail `open` — it prints a warning and continues, since the shell doesn't depend on it. A host-credential refresh that fails (usually a lapsed SSO session) is a warning too: the environment keeps whatever credentials it had and the session still opens, so recover with `erun cloud login` + [`erun cloud refresh`](/cli/cloud#cloud-refresh) when you need AWS. `erun doctor` from another shell diagnoses most cases. Full code + exit-code table: [Agent reference · CLI flag spec · `erun open` error codes](/agent-reference/cli-flags#erun-open).
