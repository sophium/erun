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
| `--type <type>` | Environment type: `local-agent` (default for a new env), `remote-agent`, or `runtime`. On an environment that already exists this **changes** the type, in either direction and between any two types; omit it and the environment keeps the type it has. |
| `--kubernetes-context <name>` | Kubernetes context to associate with the environment. |
| `--container-registry <host>` | Container registry to associate with the environment (e.g. `ghcr.io/sophium`, `<acct>.dkr.ecr.<region>.amazonaws.com`). |
| `--runtime-image <ref>` | Custom runtime image for the environment, persisted to the env config's `runtimeimage` field. Use this to run a project-built image that extends the published `erun-devops` image. |
| `--runtime-registry <host>` | Registry the environment resolves ERun's own artifacts from — the runtime chart and the platform images the pod pulls — persisted to the env config's `runtimeregistry` field. Set it when the environment's `deploy` registry holds only your project's images, so ERun's chart is not published there. |
| `--image-pull-secret <name>` | Kubernetes `dockerconfigjson` secret the runtime pod pulls its image with, persisted to the env config's `imagepullsecrets` field. Repeat or comma-separate for several. Required when `--runtime-image` names a **private** registry, since the pod cannot start without a pull credential. |
| `--set-default-tenant` | Set the initialized tenant as the default for this user. |
| `-y, --yes` | Auto-approve all initialization prompts. |
| `--components <a,b,…>` | Save this as the environment's default deploy component selection — what [`erun deploy`](/cli/deploy) rolls out with no `--components` of its own. Pass an empty string (`--components ''`) to clear a saved selection and return the environment to its repo deployment plan. |

Advanced flags (`--project-root`, `--no-git`, `--version`, `--runtime-cpu`, `--runtime-memory`, `--codecommit-ssh-key-id`, `--confirm-environment`) and the full lifecycle algorithm are on [Agent reference · CLI flag spec](/agent-reference/cli-flags#erun-init). `--remote` is a deprecated alias for `--type=remote-agent`. `--bootstrap` is deprecated and ignored — `init` no longer scaffolds a `<tenant>-devops/` module; environments deploy the published `erun-devops` chart directly, and passing the flag only prints a deprecation warning. Common root flags (`--dry-run`, `-v`/`-vv`, `--time`) apply.

## Re-running `init` on an existing environment

`init` is how you change an environment's settings after it exists. Run it again with only the flags you want to change: each one is applied, and everything you did not name is left exactly as it was — pod limits, runtime version, registries, and the type all survive an `init` that was about something else.

```bash
# Add a pull secret to an environment already running. Nothing else moves.
erun init my-tenant rihards-dev --image-pull-secret ecr-pull

# Point an environment at the registry ERun's own chart is published in
# (the way out of a deploy that cannot find the runtime chart).
erun init my-tenant rihards-dev --runtime-registry ghcr.io/sophium

# Make a runtime env an agent env, so the desktop can orchestrate work in it.
erun init my-tenant rihards-dev --type=remote-agent

# Return an environment to its repo deployment plan after a saved selection
# has been shadowing it (an empty value clears the saved selection; omitting
# the flag entirely would leave it untouched).
erun init my-tenant rihards-dev --components ''
```

Two rules make this safe to reach for:

- **A flag you passed is applied, or the command refuses and says why.** It is never accepted and quietly dropped.
- **A flag you omitted changes nothing.** `--type` in particular: without it the environment keeps its type, so an `init` about a pull secret never retypes anything.

Changing the type does the work the new type implies — retyping to `remote-agent` deploys the runtime and sets up the in-pod checkout, exactly as creating one would. Retyping **to** `local-agent` needs a host directory to mount as the worktree: run `init` from the project directory, or pass `--project-root`, or the command refuses rather than writing a type the environment could not run. Run with `--dry-run` first to see, line by line, what a re-run would change and what it would keep.

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

`init` aborts before any side effect when prerequisites are missing — `--kubernetes-context` not in kubeconfig, cwd not in a git repo, `--type=local-agent` on an existing environment with no host repo path to mount, helm-install failure. One failure lands after the pod deploys: for `remote-agent`/`runtime` types, `init` checks right after that the pod can authenticate to any ghcr.io registry it is configured to build to or deploy from, and aborts if it can't — the pod stays deployed, so authenticating it (`erun open`) and re-running `init` is the recovery, not a redeploy. Use `--dry-run` first to inspect the plan. Exact failure codes and exit-code mapping: [Agent reference · CLI flag spec · `erun init`](/agent-reference/cli-flags#erun-init).
