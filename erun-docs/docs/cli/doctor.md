---
title: erun doctor
---

# `erun doctor`

Inspect the local ERun configuration or the runtime pod state and offer recovery actions for any problems it finds.

## Synopsis

```
erun doctor [TENANT] [ENVIRONMENT] [flags]
```

## What it checks

**On the local host**, `erun doctor` validates:

- The tenant config (`~/.config/erun/<tenant>/tenant.yaml`) exists and parses.
- The environment config (`~/.config/erun/<tenant>/<env>/config.yaml`) exists and parses.
- The configured Kubernetes context exists in `~/.kube/config`.
- The runtime pod can be reached (best-effort connectivity probe).
- The project root for the tenant exists and is a git repository.

**Inside a runtime pod** (when run via SSH into the pod, detected via `ERUN_REPO_REMOTE=true`), `erun doctor` instead inspects:

- The bootstrap marker (`~/.erun/<tenant>/<env>/bootstrap.yaml`) — what init recorded.
- The project root inside the pod.
- The git checkout.
- The SSH keypair (`~/.ssh/id_ed25519`).
- The CodeCommit RSA key (when the marker recorded a CodeCommit host).

When any item is `missing`, `erun doctor` offers to run the corresponding recovery step (clone the repo, generate the SSH key, set up the CodeCommit RSA key, etc.).

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Run the inspection and print the recovery plan without performing any recovery actions. |

## Examples

Run from your laptop against the effective env:

```bash
erun doctor
```

Run inside a runtime pod after an interrupted init:

```bash
# (SSH'd into the pod)
erun doctor                # see what's missing
erun doctor --dry-run      # preview what doctor would do to fix it
```

## Exit codes

- `0` — everything is healthy (or recovery was completed successfully).
- non-zero — at least one item is missing and either recovery failed or `--dry-run` was requested.
