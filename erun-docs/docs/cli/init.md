---
title: erun init
---

# `erun init`

Initialize ERun configuration for a tenant and environment. On a local environment, `init` creates the per-user tenant/env files, scaffolds the project's `<tenant>-devops/` module if requested, and prepares the local Kubernetes context. On a remote environment, it deploys the runtime pod and writes the in-pod bootstrap marker.

## Synopsis

```
erun init [TENANT] [ENVIRONMENT] [flags]
```

If `TENANT` and/or `ENVIRONMENT` are omitted, ERun resolves them from the current working directory and the default tenant/environment in `~/.config/erun/config.yaml`. When neither can be resolved, you are prompted (or the command exits with an error in non-interactive contexts).

## Flags

| Flag | Description |
|---|---|
| `--tenant <name>` | Tenant name to initialize. |
| `--environment <name>` | Environment name. |
| `--project-root <path>` | Project root to bind to the tenant. Defaults to the current git repo root. |
| `--version <version>` | Runtime image version to initialize and deploy. Defaults to the CLI's built-in `ERUN_VERSION`. |
| `--runtime-image <repo>` | Runtime image repository (e.g. `ghcr.io/sophium/erun-devops`). Overrides the default. |
| `--runtime-cpu <value>` | Runtime pod CPU limit (e.g. `4`, `500m`). |
| `--runtime-memory <value>` | Runtime pod memory limit (e.g. `8916Mi`, `2Gi`). |
| `--kubernetes-context <name>` | Kubernetes context to associate with the environment. |
| `--container-registry <host>` | Container registry to associate with the environment (e.g. `ghcr.io/sophium`, `<acct>.dkr.ecr.<region>.amazonaws.com`). |
| `--remote` | Initialize inside the runtime pod instead of the local host (used for managed cloud environments). |
| `--no-git` | Skip remote git checkout setup. Only meaningful with `--remote`. |
| `--bootstrap` | Create the `<tenant>-devops/` module and chart during initialization. |
| `--codecommit-ssh-key-id <id>` | CodeCommit SSH public key ID, when using AWS CodeCommit as the remote git host. |
| `--set-default-tenant` | Set the initialized tenant as the default for this user. |
| `--confirm-environment` | Confirm environment initialization without prompting. |
| `-y, --yes` | Auto-approve all initialization prompts. |

Common flags from the root command (`--dry-run`, `-v`/`-vv`, `--time`) also apply.

## Examples

Initialize a local environment from inside a project:

```bash
cd ~/code/my-project
erun init my-tenant local --bootstrap --set-default-tenant
```

Initialize a remote (cloud) environment with an explicit registry:

```bash
erun init my-tenant rihards-dev \
  --remote \
  --kubernetes-context erun-004-020362606330-eu-west-2 \
  --container-registry 020362606330.dkr.ecr.eu-west-2.amazonaws.com \
  --runtime-cpu 8 \
  --runtime-memory 16Gi \
  -y
```

Dry-run to see exactly what would happen:

```bash
erun init my-tenant local --bootstrap --dry-run
```

## Side effects

- Writes `~/.config/erun/<tenant>/tenant.yaml` and `~/.config/erun/<tenant>/<env>/config.yaml`.
- Writes `<repo>/.erun/config.yaml` (project-level container registry for non-remote envs).
- With `--bootstrap`: scaffolds `<repo>/<tenant>-devops/` (Dockerfile, build.sh, helm chart skeleton).
- Deploys the runtime pod into the target Kubernetes namespace (`<tenant>-<environment>`).
- With `--remote`: writes the in-pod marker at `/home/erun/.erun/<tenant>/<env>/bootstrap.yaml`.
