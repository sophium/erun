---
title: erun sshd
---

# `erun sshd`

Enable SSH access to a remote environment — the prerequisite for attaching an IDE (`erun open --vscode` / `--intellij`) or connecting over plain SSH.

## Synopsis

```
erun sshd init [TENANT] [ENVIRONMENT] [flags]
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

## Error behaviour

| Failure | Behaviour |
|---|---|
| Environment is local (not remote). | Errors — SSH only applies to a remote environment. |
| No Kubernetes context configured. | Errors before any change. |
| No SSH public key found and none given. | Errors asking for `--public-key`. |
| Pod not ready when pushing the key. | Retries a few times, then errors. |
