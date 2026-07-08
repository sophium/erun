---
title: erun deploy
---

# `erun deploy`

Install a published version into a Kubernetes environment. `erun deploy` is a **pure consume** step — it helm-installs the image and chart that [`erun push`](/cli/push) already published at a version, addressing them by reference. It never builds, pushes, or publishes. It's the deploy step of the [delivery pipeline](/pipeline); see [Command primitives](/concepts/command-primitives) for how it composes with `build` and `push`.

## Synopsis

```
erun deploy [TENANT] [ENVIRONMENT] [flags]
```

If `TENANT` and/or `ENVIRONMENT` are omitted, they resolve from defaults (same way as `erun open`).

A version is **required**: pass `--version <v>` to install a specific published version, or `--current` to redeploy the version the environment already runs. Running `erun deploy` with neither errors — `deploy` does not mint a version, so there is nothing for it to install. Versions are minted only by [`erun build`](/cli/build); to produce a new one, build and push it first (or let the desktop app, which composes those steps for you).

## What gets deployed

`erun deploy` is **opt-in**: it rolls out exactly the charts you select and nothing else. The selection resolves by precedence — the `--components` flag first, then the environment's saved default (set in the desktop app's Runtime tab), then the `.erun/config.yaml` deployment plan. When none of those name anything, deploy rolls out the environment's runtime chart alone, which bootstraps or heals it.

The deployment plan also sets ordering: steps run in order, and a list within a step deploys in parallel; when the plan is absent, deploy falls back to chart-dependency-based ordering. For the full precedence rules and the plan's YAML schema, see [Configuration · `environments.<env>.k8s.deployments[]`](/reference/configuration#per-project-config) and [Agent reference · CLI flag spec · `erun deploy`](/agent-reference/cli-flags#erun-deploy).

## Where the runtime chart comes from

When the project repo carries its own runtime chart (`<tenant>-devops/k8s/<tenant>-devops/`, or the [`paths.k8s`](/reference/configuration#paths-block) directory when configured), that repo-local chart is what gets deployed — nothing changes for projects that have one. Environments **without** a repo-local runtime chart — every remote env, and any env whose project has no `<tenant>-devops` chart — deploy the published chart directly. Deploy prefers the tenant's own published `charts/<tenant>-devops` — a thin umbrella that wraps `erun-devops` (added by `erun-build-env` only when the pod shape needs a sidecar, extra volume, or RBAC) — when that version exists in the registry, and falls back to the shared `charts/erun-devops` when the tenant publishes none — the published mirror of the repo-local `<tenant>-devops`-first lookup:

```
helm upgrade --install <tenant>-devops oci://<registry>/charts/<tenant>-devops --version <runtime version> …
```

Component charts resolve the same way. On a sourceless env (a remote/runtime env), a selected component chart with no repo-local copy installs by reference from the registry — `helm upgrade --install <chart> oci://<registry>/charts/<chart> --version <v>` — including a tenant's own charts (`frs-backend-api`, `frs-powerdns`, …) that [`erun push`](/cli/push) published. The sourceless path trusts the selection (there are no local charts to validate against); a name whose chart was never published surfaces at deploy time as an actionable "that version has no published chart" error rather than being rejected up front.

A tenant's own chart is usually a thin **umbrella** that wraps the canonical `erun-<component>` chart (or, for the runtime, `erun-devops`) as a subchart. helm won't pass top-level values into a wrapped subchart, so a by-reference deploy of an umbrella forwards them for you: it re-scopes the values it sets under the subchart key **and** pulls the published chart to apply its bundled `values.<env>.yaml` — so the subchart's required `tenant`/`environment` (and your own per-env overrides authored under that key) are satisfied without a worktree. You'll see a `helm pull … --untar` step ahead of the `helm upgrade` in the dry-run. See [Agent reference · `erun deploy`](/agent-reference/cli-flags#deploy-subchart-forwarding) for the exact forwarding and precedence rules.

The chart and runtime image are one contract — published together to the same registry at the same version. [`erun push`](/cli/push) publishes both at once, so whatever version `deploy` is handed already has a matching chart waiting; `deploy` only pulls and installs it, never publishes. Because push publishes the chart for *every* version it pushes — snapshot or release — any pushed version is deployable, with no chart-versus-image gap (see [Release flow](/deployment/release-flow)). The cluster pulls from the `deploy`-marked registry in the env's [registry list](/deployment/registries) (the env's recorded runtime registry wins as provenance); when the list marks a `from` and a `to`, ERun copies the image there first. The dry-run trace names the decision: `deploy: no local runtime chart; using published chart <ref> version <v>`. To customise a published-chart deploy, use the env's values overlay and the `runtimeimage` field — see [Configuration · Advanced chart values](/reference/configuration#advanced-chart-values).

### Bootstrapping from the ERun base image {#runtime-image-bootstrap}

A brand-new tenant environment has no `<tenant>-devops` image of its own yet — nothing to install by reference. `--runtime-image` lets you get it running on the canonical ERun base image first:

```bash
erun deploy team dev --version 1.2.3 --runtime-image ghcr.io/sophium/erun-devops
```

This installs the published `erun-devops` chart with `ghcr.io/sophium/erun-devops:1.2.3` as the runtime image, **bypassing any repo-local `<tenant>-devops` chart**. The chosen image is recorded as the env's runtime image, so a later `open` or `deploy --current` reuses it. Once you have built and pushed the env's own image, deploy that version without the flag (or pick the tenant image in the desktop runtime dialog) to switch over. The desktop's version picker offers both the ERun base image and the env's own image and threads this flag for you when you pick the base.

## Flags

| Flag | Description |
|---|---|
| `--version <version>` | The published version to install, by reference. Required unless `--current` is given. The version's image and chart must already exist (locally or in the registry) or the deploy errors — `deploy` never builds them. |
| `--current` | Redeploy the version the environment is already recorded as running (its persisted runtime version). Use it to re-roll the same version, or after a `--force`-style retry, without retyping the number. Required unless `--version` is given. |
| `--runtime-image <ref>` | Install the runtime running this image via the published `erun-devops` chart (`imageOverrides.erun-devops`), pinned to `--version`, **even when the env has a repo-local runtime chart** (which it bypasses). Use it to [bootstrap an env on the canonical ERun base image](#runtime-image-bootstrap) before its own image is built; mirrors [`erun open --runtime-image`](/cli/open). |
| `--components <name,name,...>` | The exact charts to deploy this run — chart directory names under `<tenant>-devops/k8s/`, plus the runtime release name `<tenant>-devops`. Overrides the env's saved selection and the deployment plan for this run; an unknown name (matching no chart and no runtime alias) is rejected with `unknown deploy component`. See [Agent reference · `--components`](/agent-reference/cli-flags#components-value-set). |
| `--force` | Re-run helm upgrade even when the resolved version is unchanged and nothing needs rolling. |
| `--rollout-timeout <dur>` | How long to wait for the rollout before giving up (e.g. `10m`). Defaults to the env's setting, or 5 minutes. Raise it for the first deploy of a large image on a slow connection; see [rollout wait and monitoring](/agent-reference/cli-flags#rollout-wait-and-pod-monitoring). |
| `--dry-run` | Resolve and print every command it would run — `helm dependency build` for local umbrella charts, `helm pull --untar` for a by-reference umbrella's bundled values, then `helm upgrade --install` (and any image-copy step) — without executing. |

Subcommand:

| Command | Description |
|---|---|
| `erun deploy COMPONENT` | Install a single component's helm chart directly at the resolved version (no plan resolution). Still requires `--version` or `--current`. |

## What deploy installs {#what-deploy-installs}

`erun deploy` *consumes* an already-published version — it never produces one. A version is a content identity, not a label, so ERun addresses the published image and chart by reference: it does **not** build your working tree, and it does **not** push (so it can never overwrite the published `<v>`). Before the rollout it checks that the version's image and chart exist; if they don't, the deploy errors instead of building or publishing them. When a selected chart is an umbrella that wraps published subcharts (the [platform blueprint](/concepts/skills) pattern, where `<tenant>-<component>` depends on the published `erun-<component>` chart), deploy runs `helm dependency build` first so the subcharts are present before install — it pulls the already-published dependency charts pinned in `Chart.lock`, still never building them. `erun upgrade` rolls a fleet forward by calling deploy this same way.

You always say *which* version:

- **`--version <v>`** — install a specific published version. Use it to roll forward to a new build, or to roll back to an earlier one.
- **`--current`** — reinstall the version the environment already runs (its persisted runtime version). Use it to re-roll the same version without retyping the number.

To produce a *new* version from your working tree, build and push it first ([`erun build`](/cli/build) mints it, [`erun push`](/cli/push) publishes it), then `deploy --version` that version. The desktop app composes those steps for you; the `build --deploy` shortcut runs them end to end for an Operator at the terminal (see [Command primitives](/concepts/command-primitives)).

## Skipping helm when nothing changed

When the resolved version matches what the environment already runs and the chart didn't change, `erun deploy` skips the redundant `helm upgrade --install`. Pass `--force` to override.

This means a no-op `erun deploy --current` is essentially free.

## The database reset

A deploy that includes the `erun-backend-postgres` component **resets the environment's Postgres database** — convenient for a throwaway local stack, surprising if you didn't expect it. The reset rides in the postgres chart itself, so it still runs when the version is unchanged and `deploy` would otherwise skip that chart's helm step (the dry-run trace names the decision). Reserve the postgres component for agent and throwaway envs; runtime envs that must keep their data should not include it.

On a successful deploy of the runtime chart, the resolved version and registry are persisted to the environment's config, so the next `open`, `deploy --current`, or `upgrade` reuses them.

## Examples

Install a published version into the default tenant/environment:

```bash
erun deploy --version 1.2.3
```

Redeploy the version the environment already runs:

```bash
erun deploy --current
```

Bootstrap a new environment on the canonical ERun base image (before its own image is built):

```bash
erun deploy team dev --version 1.2.3 --runtime-image ghcr.io/sophium/erun-devops
```

Install a version plus the backend stack:

```bash
erun deploy --version 1.2.3 --components erun-backend-postgres,erun-backend-db,erun-backend-api
```

Dry-run to inspect the plan:

```bash
erun deploy --version 1.2.3 --dry-run --components erun-backend-postgres
```

Deploy a single component chart at a version:

```bash
erun deploy erun-backend-api --version 1.2.3
```

Install a published version into a specific environment:

```bash
erun deploy team prod --version 1.2.3
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Neither `--version` nor `--current` given. | Errors before any change: `deploy requires a version — pass --version <v> or --current`. `deploy` never builds, so there is nothing to install without one. Exit code 1. |
| Cluster unreachable. | Errors before any change; exit code 1, message identifies the context. |
| Linked cloud context is stopped. | Starts the context, waits for readiness, then proceeds. If start fails, errors. |
| `--version <v>` names a version whose image was never published. | Errors during resolution, before `helm upgrade`: `image <ref> is not present locally or in the registry; deploy installs an existing version and does not build it — run erun build/push to create it first`. No build, no push, no partial deploy. |
| `--current` but the environment has no recorded version yet. | Errors before any change — there is no current version to redeploy. Deploy a specific `--version` once to seed it. |
| Runtime chart isn't published at the requested version. | Errors with `runtime chart <ref> version <v> could not be pulled from <registry>` and how to recover — push that version first (push publishes image and chart together), then redeploy. No partial deploy. |
| A Deployment's selector changed (an immutable Kubernetes field) — e.g. upgrading an environment first deployed under an older chart. | `deploy` recreates that Deployment for you: it deletes it and retries the upgrade once. The Deployment's data volumes (PVCs — build cache, home directory) are separate objects and survive the delete, so no data is lost; the pod restarts. No manual `helm` surgery needed. The trace names it: `deploy: Deployment <name> selector is immutable and changed; deleting it (PVCs preserved) and retrying the upgrade`. |
| Helm upgrade fails on step N. | The plan stops at step N. Steps 1..N-1 are committed; step N is in helm's failure state. Fix and rerun, or `helm rollback` that release. The rest of the plan is left untouched. |
| A pod's image is still pulling when the rollout starts. | `deploy` keeps waiting (up to the rollout timeout) and shows `Pulling image (...)` lines — a large image on a slow or rate-limited registry is normal. Raise `--rollout-timeout` (or the env's deploy timeout) if the first pull of a fresh image regularly outlasts the default. |
| A pod hits a real failure — crash loop, bad config, or a missing/denied image. | `deploy` stops immediately rather than waiting out the timeout, naming the pod, container, and reason (`deploy failed early: …`). Fix the cause (push the image, fix the config) and rerun. See [rollout wait and monitoring](/agent-reference/cli-flags#rollout-wait-and-pod-monitoring). |
| `erun deploy <component>` for a component not in the plan. | Deploys the single component directly — that's the documented bypass. No error. |

`erun deploy --dry-run` prints the exact command sequence ahead of time, so the Operator can preview the plan before committing.
