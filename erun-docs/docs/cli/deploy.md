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

## Where the runtime chart comes from

When the project repo carries its own runtime chart (`<tenant>-devops/k8s/<tenant>-devops/`), that repo-local chart is what gets deployed — nothing changes for projects that have one. Environments **without** a repo-local runtime chart — every remote env, and any env whose project has no `<tenant>-devops` chart — deploy the published chart directly:

```
helm upgrade --install <tenant>-devops oci://<registry>/charts/erun-devops --version <runtime version> …
```

The chart and runtime image are one contract — published together to the same registry at the same version. A release publishes both; so does any `erun deploy` (or `build --deploy`) that builds and pushes the runtime image, which publishes the matching chart to `oci://<registry>/charts/erun-devops` at the same version. That keeps a pushed snapshot deployable instead of leaving an image with no chart for a published-chart env to pull (see [Release flow](/deployment/release-flow)). The cluster pulls from the `deploy`-marked registry in the env's [registry list](/deployment/registries) (the env's recorded runtime registry wins as provenance); when the list marks a `from` and a `to`, ERun copies the image there first. The dry-run trace names the decision: `deploy: no local runtime chart; using published chart <ref> version <v>`. To customise a published-chart deploy, use the env's values overlay and the `runtimeimage` field — see [Configuration · Advanced chart values](/reference/configuration#advanced-chart-values).

## Flags

| Flag | Description |
|---|---|
| `--components <name,name,...>` | Opt-in components to include alongside the runtime chart. The accepted list is derived from each project's `<tenant>-devops/k8s/<component>/` charts. |
| `--version <version>` | Install the image and chart already published at `<version>` by reference, instead of building from the working tree. The version's image must already exist (locally or in the registry) or the deploy errors — see [build vs install](#building-vs-installing-an-existing-version). |
| `--publish` | Package and push each resolved chart to the environment's container registry as an OCI Helm artifact before the upgrade. |
| `--force` | Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected. |
| `--dry-run` | Resolve and print every `docker`, `docker push`, and `helm upgrade --install` command without executing. |

Subcommand:

| Command | Description |
|---|---|
| `erun deploy COMPONENT` | Deploy a single component's helm chart directly (no plan resolution). |

## Building vs installing an existing version {#building-vs-installing-an-existing-version}

`erun deploy` has two modes, chosen by whether you pass `--version`:

- **No `--version` (build):** in an agent env, `erun deploy` builds the images the charts need from your working tree, pushes them, and rolls them out. This is the default delivery loop — the deploy *produces* a new version.
- **With `--version <v>` (install):** `erun deploy` *consumes* an existing version. A version is a content identity, not a label, so ERun addresses the already-published image by reference — it does **not** rebuild your working tree under that version, and it does **not** push (so it can never overwrite the published `<v>`). Before the rollout it checks that the version's image exists; if it doesn't, the deploy errors instead of building one. `erun upgrade` deploys this same way.

Use `--version` to redeploy or roll back to a version that was already built and pushed (by a release, or an earlier `erun build` / `build --deploy`). To produce a *new* version from your working tree, run `erun deploy` without `--version`, or use the build flow.

## Skipping helm when nothing changed

When all of a chart's locally-built images were promoted from the fingerprint cache (no rebuild) and the chart itself didn't change, `erun deploy` skips both the redundant `docker push` and the `helm upgrade --install` for that chart. Pass `--force` to override.

This means a no-op `erun deploy` after a clean clone is essentially free.

## Snapshot builds and the database

Whether `deploy` builds and deploys local snapshot images is decided by the env's [type](/concepts/environment-types): agent environments (`local-agent`, `remote-agent`) build here; runtime environments deploy released images from the registry and don't build. There is no `--snapshot` flag — set the env's type instead. A snapshot deploy (an agent env) that includes the `erun-backend-postgres` component also **resets the environment's Postgres database** — convenient for a throwaway local stack, surprising if you didn't expect it. The reset rides in the postgres chart itself, so it still runs when image caching would otherwise skip that chart's helm step (the dry-run trace names the decision). Runtime environments don't reset data.

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

Install an already-published version (no build — fails if that version isn't published):

```bash
erun deploy team prod --version 1.2.3
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Cluster unreachable. | Errors before any change; exit code 1, message identifies the context. |
| Linked cloud context is stopped. | Starts the context, waits for readiness, then proceeds. If start fails, errors. |
| Referenced image isn't in the registry. | Errors before `helm upgrade`; logs the missing image and the resolved registry. No partial deploy. |
| `--version <v>` names a version whose image was never built. | Errors during resolution, before `helm upgrade`: `image <ref> is not present locally or in the registry; deploy installs an existing version and does not build it — run erun build/push to create it first`. No build, no push, no partial deploy. |
| Runtime chart isn't published at the requested version. | Errors with `runtime chart <ref> version <v> could not be pulled from <registry>` and how to recover — deploy a released version, or publish the chart (`erun deploy --publish` / a push-deploy). Common when a snapshot image was pushed but its chart never was. No partial deploy. |
| Helm upgrade fails on step N. | The plan stops at step N. Steps 1..N-1 are committed; step N is in helm's failure state. Fix and rerun, or `helm rollback` that release. The rest of the plan is left untouched. |
| `erun deploy <component>` for a component not in the plan. | Deploys the single component directly — that's the documented bypass. No error. |
| Stale fingerprint cache. | Cache misses silently and the build/push runs as if it weren't cached. Use `--force` to bypass it explicitly. |

`erun deploy --dry-run` prints the exact command sequence ahead of time, so the Operator can preview the plan before committing.
