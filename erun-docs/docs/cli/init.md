---
title: erun init
---

# `erun init`

Initialize ERun configuration for a tenant and environment. On a local environment, `init` creates the per-user tenant/env files and prepares the local Kubernetes context. On a remote environment, it deploys the runtime pod — straight from the published `erun-devops` chart — and writes the in-pod bootstrap marker. `init` does not generate any files into your project beyond `.erun/config.yaml`; the runtime chart and image ship as release artifacts, and projects that need a custom toolchain extend the published image instead (see [`--runtime-image`](#flags) below).

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
| `--type <type>` | Environment type: `local-agent` (default), `remote-agent`, or `runtime`. |
| `--kubernetes-context <name>` | Kubernetes context to associate with the environment. |
| `--container-registry <host>` | Container registry to associate with the environment (e.g. `ghcr.io/sophium`, `<acct>.dkr.ecr.<region>.amazonaws.com`). |
| `--runtime-image <ref>` | Custom runtime image for the environment, persisted to the env config's `runtimeimage` field. Use this to run a project-built image that extends the published `erun-devops` image. |
| `--set-default-tenant` | Set the initialized tenant as the default for this user. |
| `-y, --yes` | Auto-approve all initialization prompts. |

Advanced flags (`--project-root`, `--no-git`, `--version`, `--runtime-cpu`, `--runtime-memory`, `--codecommit-ssh-key-id`, `--confirm-environment`) and the full lifecycle algorithm are on [Agent reference · CLI flag spec](/agent-reference/cli-flags#erun-init). `--remote` is a deprecated alias for `--type=remote-agent`. `--bootstrap` is deprecated and ignored — `init` no longer scaffolds a `<tenant>-devops/` module; environments deploy the published `erun-devops` chart directly, and passing the flag only prints a deprecation warning. Common root flags (`--dry-run`, `-v`/`-vv`, `--time`) apply.

## Examples

Initialize a local environment from inside a project:

```bash
cd ~/code/my-project
erun init my-tenant local --set-default-tenant
```

Initialize a remote (cloud) environment with an explicit registry:

```bash
erun init my-tenant rihards-dev \
  --type=remote-agent \
  --kubernetes-context erun-004-020362606330-eu-west-2 \
  --container-registry 020362606330.dkr.ecr.eu-west-2.amazonaws.com \
  --runtime-cpu 8 \
  --runtime-memory 16Gi \
  -y
```

Dry-run to see exactly what would happen:

```bash
erun init my-tenant local --dry-run
```

## Side effects

`init` writes per-user tenant + env config under `~/.config/erun/`, a project-level `<repo>/.erun/config.yaml`, and deploys the runtime pod into the `<tenant>-<environment>` namespace. The runtime pod comes from the published `erun-devops` chart and image — `init` writes nothing else into your project.

## Error behaviour

`init` aborts before any side effect when prerequisites are missing — `--kubernetes-context` not in kubeconfig, cwd not in a git repo, helm-install failure. Use `--dry-run` first to inspect the plan. Exact failure codes and exit-code mapping: [Agent reference · CLI flag spec · `erun init`](/agent-reference/cli-flags#erun-init).
