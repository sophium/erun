---
title: erun deploy
---

# `erun deploy`

Build (if needed), push, and roll out the helm chart for the current devops module. `erun deploy` is the single-command workflow for moving code from your machine into a Kubernetes environment.

## Synopsis

```
erun deploy [TENANT] [ENVIRONMENT] [flags]
```

If `TENANT` and/or `ENVIRONMENT` are omitted, they resolve from defaults (same way as `erun open`).

## Deployment plan

Each environment declares its deployment plan in `.erun/config.yaml` under `environments.<env>.k8s.deployments`. Each item is either a single component name or a list of names (deployed in parallel within that step).

Example:

```yaml
environments:
  local:
    k8s:
      deployments:
        - [erun-devops, erun-backend-postgres]   # parallel first step
        - erun-backend-db                         # waits for postgres
        - erun-backend-api                        # waits for db migration
```

When an environment has no `k8s` section, `erun deploy` falls back to the default rank: `erun-backend-postgres → erun-backend-db → erun-backend-api → other`.

## Flags

| Flag | Description |
|---|---|
| `--components <name,name,...>` | Opt-in components to include alongside the runtime chart. Valid values: `erun-backend-postgres`, `erun-backend-db`, `erun-backend-api`. |
| `--version <version>` | Override the deployed chart and image version. |
| `--force` | Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected. |
| `--dry-run` | Resolve and print every `docker`, `docker push`, and `helm upgrade --install` command without executing. |

Subcommand:

| Command | Description |
|---|---|
| `erun deploy COMPONENT` | Deploy a single component's helm chart directly (no plan resolution). |

## Skipping helm when nothing changed

When all of a chart's locally-built images were promoted from the fingerprint cache (no rebuild) and the chart itself didn't change, `erun deploy` skips both the redundant `docker push` and the `helm upgrade --install` for that chart. Pass `--force` to override.

This means a no-op `erun deploy` after a clean clone is essentially free.

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
