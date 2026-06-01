---
title: erun deploy
---

# `erun deploy`

Build (if needed), push, and roll out the helm chart for the current devops module. `erun deploy` is the single-command workflow for moving code from your machine into a Kubernetes environment — the deploy step of the [delivery pipeline](/pipeline).

## Synopsis

```
erun deploy [TENANT] [ENVIRONMENT] [flags]
```

If `TENANT` and/or `ENVIRONMENT` are omitted, they resolve from defaults (same way as `erun open`).

## Deployment plan

Each environment declares its deployment plan in `.erun/config.yaml`. Steps run in order; a list within a step is deployed in parallel. When the plan is absent, `erun deploy` falls back to chart-dependency-based ordering. For the full YAML schema and resolution rules, see [Configuration · `environments.<env>.k8s.deployments[]`](/reference/configuration#per-project-config) and [Agent reference · CLI flag spec · `erun deploy`](/agent-reference/cli-flags#erun-deploy).

## Flags

| Flag | Description |
|---|---|
| `--components <name,name,...>` | Opt-in components to include alongside the runtime chart. The accepted list is derived from each project's `<tenant>-devops/k8s/<component>/` charts. |
| `--version <version>` | Override the deployed chart and image version. |
| `--snapshot` / `--no-snapshot` | Build and deploy local snapshot images in a local environment (on by default there). A snapshot deploy also **resets the environment's Postgres database**. |
| `--publish` | Package and push each resolved chart to the environment's container registry as an OCI Helm artifact before the upgrade. |
| `--force` | Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected. |
| `--dry-run` | Resolve and print every `docker`, `docker push`, and `helm upgrade --install` command without executing. |

Subcommand:

| Command | Description |
|---|---|
| `erun deploy COMPONENT` | Deploy a single component's helm chart directly (no plan resolution). |

## Skipping helm when nothing changed

When all of a chart's locally-built images were promoted from the fingerprint cache (no rebuild) and the chart itself didn't change, `erun deploy` skips both the redundant `docker push` and the `helm upgrade --install` for that chart. Pass `--force` to override.

This means a no-op `erun deploy` after a clean clone is essentially free.

## Snapshot mode and the database

In a local environment, `deploy` builds and deploys local snapshot images by default (`--no-snapshot` opts out). A snapshot deploy also **resets the environment's Postgres database** — convenient for a throwaway local stack, surprising if you didn't expect it. Runtime environments deploy released images from the registry and don't reset data.

On a successful deploy of the runtime chart, the resolved version and registry are persisted to the environment's config, so the next `open` / `deploy` reuses them.

## Examples

Deploy the default runtime chart only:

```bash
erun deploy
```

Deploy the runtime plus the backend stack:

```bash
erun deploy --components erun-backend-postgres,erun-backend-db,erun-backend-api
```

Dry-run to inspect the plan:

```bash
erun deploy --dry-run --components erun-backend-postgres
```

Deploy a specific component chart:

```bash
erun deploy erun-backend-api
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Cluster unreachable. | Errors before any change; exit code 1, message identifies the context. |
| Linked cloud context is stopped. | Starts the context, waits for readiness, then proceeds. If start fails, errors. |
| Referenced image isn't in the registry. | Errors before `helm upgrade`; logs the missing image and the resolved registry. No partial deploy. |
| Helm upgrade fails on step N. | The plan stops at step N. Steps 1..N-1 are committed; step N is in helm's failure state. Fix and rerun, or `helm rollback` that release. The rest of the plan is left untouched. |
| `erun deploy <component>` for a component not in the plan. | Deploys the single component directly — that's the documented bypass. No error. |
| Stale fingerprint cache. | Cache misses silently and the build/push runs as if it weren't cached. Use `--force` to bypass it explicitly. |

`erun deploy --dry-run` prints the exact command sequence ahead of time, so the Operator can preview the plan before committing.
