---
title: erun sshd
---

# `erun sshd`

Enable SSH access to a remote environment — the prerequisite for attaching an IDE (`erun open --vscode` / `--intellij`) or connecting over plain SSH.

## Synopsis

```
erun sshd init [TENANT] [ENVIRONMENT] [flags]
erun sshd sync [TENANT] [ENVIRONMENT] [flags]
```

## What it does

1. Records SSHD as enabled in the environment's config and resolves the public key to authorize (auto-discovers `~/.ssh/id_ed25519.pub` → `id_ecdsa.pub` → `id_rsa.pub` if `--public-key` isn't given).
2. **Redeploys the runtime chart** with sshd turned on — this is what starts `sshd` inside the pod.
3. Pushes your public key into the pod's `authorized_keys`.
4. Writes a host entry to your `~/.ssh/config` (alias `erun-<tenant>-<env>`, user `erun`) so you can `ssh erun-<tenant>-<env>` directly.

The port-forward that makes the connection reachable is started later, when the environment is opened.

## Flags

| Flag | Description |
|---|---|
| `--public-key` | Public key to authorize (defaults to an auto-discovered key). |
| `--local-port` | Fixed local port for the forward (defaults to the env's allocated SSH port). |
| `--tenant`, `--environment` | Target a specific tenant/environment. |

## Examples

```bash
erun sshd init my-tenant rihards-dev --dry-run
erun sshd init my-tenant rihards-dev
erun open my-tenant rihards-dev --vscode   # now possible
```

## `erun sshd sync`

Runs one workspace-sync pass for a `remote-agent` environment: mirrors the pod's git-visible worktree into the host review directory, deletes what the pod no longer has, and delivers the pod's cross-built artifacts into the mirror's `.erun-outputs/`. See the [workspace sync spec](/agent-reference/workspace-sync-spec) for the pass itself.

The desktop runs the same pass on a poller. This command exists so the pass is reachable without the desktop — an orchestrator whose mirror is empty or stale refreshes it in one call instead of working around it.

```bash
erun sshd sync my-tenant rihards-dev --dry-run   # counts only, mirror untouched
erun sshd sync my-tenant rihards-dev
```

`--dry-run` resolves the pass, traces the pod path and the host path it maps to, and reports the counts a real pass would change without creating, fetching, or deleting anything.

It refuses, naming which precondition failed, when the environment has no pod worktree (not a `remote-agent` env), has workspace sync disabled, has no configured local path, or when its SSH channel is not up.

## Error behaviour

| Failure | Behaviour |
|---|---|
| Environment is local (not remote). | Errors — SSH only applies to a remote environment. |
| No Kubernetes context configured. | Errors before any change. |
| No SSH public key found and none given. | Errors asking for `--public-key`. |
| Pod not ready when pushing the key. | Retries a few times, then errors. |
