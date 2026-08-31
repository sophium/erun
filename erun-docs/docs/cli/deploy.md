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

`erun deploy` is **opt-in**: it rolls out exactly the charts you select and nothing else. The selection resolves by precedence — the `--components` flag first, then the environment's saved default (set with `erun init --components`, or from the desktop app's Runtime tab), then the `.erun/config.yaml` deployment plan. When none of those name anything, deploy rolls out the environment's runtime chart alone, which bootstraps or heals it.

A saved default wins over the plan permanently, so if the plan has grown since the selection was saved, a plain deploy (no `--components`) **refuses** rather than silently rolling out only the stale saved subset — it names what the plan asks for beyond the saved set and both ways to reconcile: adopt the addition (`erun init --components <a,b,…>` naming the full set), or clear the saved selection (`erun init --components ''`) and return the environment to the plan. Passing `--components` explicitly for that run bypasses this entirely, same as it bypasses the saved selection itself.

The deployment plan also sets ordering: steps run in order, and a list within a step deploys in parallel; when the plan is absent, deploy falls back to chart-dependency-based ordering. For the full precedence rules and the plan's YAML schema, see [Configuration · `environments.<env>.k8s.deployments[]`](/reference/configuration#per-project-config) and [Agent reference · CLI flag spec · `erun deploy`](/agent-reference/cli-flags#erun-deploy).

## Where the runtime chart comes from

When the project repo carries its own runtime chart (`<tenant>-devops/k8s/<tenant>-devops/`, or the [`paths.k8s`](/reference/configuration#paths-block) directory when configured), that repo-local chart is what gets deployed — nothing changes for projects that have one. Environments **without** a repo-local runtime chart — every remote env, and any env whose project has no `<tenant>-devops` chart — deploy the published chart directly:

```
helm upgrade --install <tenant>-devops oci://<registry>/charts/<tenant>-devops --version <runtime version> …
```

Deploy searches for that chart in order, and installs the first coordinate that publishes the version:

1. **`charts/<tenant>-devops` in the environment's chart registry** — the tenant's own umbrella, a thin chart wrapping `erun-devops` (added by `erun-build-env` only when the pod shape needs a sidecar, extra volume, or RBAC). Preferred whenever it exists, the published mirror of the repo-local `<tenant>-devops`-first lookup.
2. **`charts/erun-devops` in the same registry** — the shared platform chart, for a project that rides ERun's runtime as-is and publishes its chart alongside its images.
3. **`charts/erun-devops` in the registry the runtime image comes from** — where ERun actually releases the platform chart. This rung is what makes a project deployable when its `deploy` registry holds only its own application images: a private ECR with no `charts/*` repository has ERun's chart at no version, and stopping at step 2 left such an environment undeployable at every version.

The chart registry in steps 1–2 is the env's `runtimeregistry` when it records one, otherwise its `deploy`-marked registry. Step 3 is the registry of the env's `runtimeimage`, or `ghcr.io/sophium` when it names none. The dry-run traces every rung it tried and passed over, so the search is auditable before you roll:

```
deploy: runtime chart acme-devops 1.0.178 not found in <acct>.dkr.ecr.eu-west-2.amazonaws.com (the tenant's own umbrella)
deploy: runtime chart erun-devops 1.0.178 not found in <acct>.dkr.ecr.eu-west-2.amazonaws.com (the shared platform chart)
deploy: runtime chart erun-devops 1.0.178 found in ghcr.io/sophium (the shared platform chart in erun's own registry)
```

When no rung is confirmed at the requested version, deploy **refuses** rather than guess — it never installs the shared `erun-devops` chart at another project's version, because that chart is versioned on ERun's own release line and the pair can never exist. The refusal names every coordinate it probed and whether each was confirmed absent or simply couldn't be read (an unreadable registry is never treated as a "no"), along with the ways out: [`erun init --runtime-registry <host>`](/cli/init) to record the registry ERun's artifacts come from, publishing your own umbrella at that version, or [naming the chart outright](#runtime-chart-coordinate).

A successful deploy remembers the rung it resolved at: it records that registry as the env's [`runtimeregistry`](/reference/configuration#envconfig), so the next search starts there instead of walking the same ladder again. It only ever fills that field in or confirms it — a value you set with `erun init --runtime-registry` is never replaced by a deploy, even when the chart resolves somewhere else. That case is traced rather than applied, and the trace names the command that would change it:

```
deploy: the env's runtime registry ghcr.io/acme stands; the runtime chart resolved from ghcr.io/sophium instead (`erun init acme prod --runtime-registry ghcr.io/sophium` changes it)
```

Component charts resolve the same way. On a sourceless env (a remote/runtime env), a selected component chart with no repo-local copy installs by reference from the registry — `helm upgrade --install <chart> oci://<registry>/charts/<chart> --version <v>` — including a tenant's own charts (`frs-backend-api`, `frs-powerdns`, …) that [`erun push`](/cli/push) published. The sourceless path trusts the selection (there are no local charts to validate against); a name whose chart was never published surfaces at deploy time as an actionable "that version has no published chart" error rather than being rejected up front.

A tenant's own chart is usually a thin **umbrella** that wraps the canonical `erun-<component>` chart (or, for the runtime, `erun-devops`) as a subchart. helm won't pass top-level values into a wrapped subchart, so a by-reference deploy of an umbrella forwards them for you: it re-scopes the values it sets under the subchart key **and** pulls the published chart to apply its bundled `values.<env>.yaml` — so the subchart's required `tenant`/`environment` (and your own per-env overrides authored under that key) are satisfied without a worktree. You'll see a `helm pull … --untar` step ahead of the `helm upgrade` in the dry-run. See [Agent reference · `erun deploy`](/agent-reference/cli-flags#deploy-subchart-forwarding) for the exact forwarding and precedence rules.

The chart and runtime image are one contract — published together to the same registry at the same version. [`erun push`](/cli/push) publishes both at once, so whatever version `deploy` is handed already has a matching chart waiting; `deploy` only pulls and installs it, never publishes. Because push publishes the chart for *every* version it pushes — snapshot or release — any pushed version is deployable, with no chart-versus-image gap (see [Release flow](/deployment/release-flow)). When a project versions its runtime image on its own release line rather than ERun’s, the pair comes apart and the chart has to be [named separately](#runtime-chart-coordinate). The cluster pulls from the `deploy`-marked registry in the env's [registry list](/deployment/registries) (the env's recorded runtime registry wins as provenance); when the list marks a `from` and a `to`, ERun copies the image there first. The dry-run trace names the decision: `deploy: no local runtime chart; using published chart <ref> version <v>`.

Because the umbrella and its `<tenant>-devops` image are published together, a deploy of the tenant's own `charts/<tenant>-devops` also **defaults the runtime image to that umbrella's own image** — `<registry>/<tenant>-devops:<version>` — with no `runtimeimage` to set: building and pushing the image is enough for the deploy to run it (the dry-run traces `deploy: defaulting runtime image to the <tenant>-devops chart's own image …`). A deploy of the shared `charts/erun-devops` chart has no such signal, so an image-only build env still points at its image through `runtimeimage`. To customise either, use the env's values overlay and the `runtimeimage` field — see [Configuration · Advanced chart values](/reference/configuration#advanced-chart-values). When a tenant's umbrella image is a **private** `ghcr.io` package, `deploy` figures that out on its own: it needs no `imagepullsecrets` set up front, resolves a pull credential the same way `erun push`/`erun release` do, and auto-provisions and attaches a secret when one resolves. When none resolves and the image can't be confirmed public, `deploy` **refuses before the rollout** rather than tearing down the running pod for one that can't be pulled — see [Configuration · Private image pull secrets](/reference/configuration#advanced-image-pull-secrets) and [Troubleshooting · image is not anonymously pullable](/reference/troubleshooting#image-not-anonymously-pullable). And a stale `runtimeimage` still pointing at the stock `erun-devops` image is ignored on an umbrella deploy (it would pin a tag the tenant's version line never published) in favour of the umbrella's own image.

### Bootstrapping from the ERun base image {#runtime-image-bootstrap}

A brand-new tenant environment has no `<tenant>-devops` image of its own yet — nothing to install by reference. `--runtime-image` lets you get it running on the canonical ERun base image first:

```bash
erun deploy team dev --version 1.2.3 --runtime-image ghcr.io/sophium/erun-devops
```

This installs the published `erun-devops` chart with `ghcr.io/sophium/erun-devops:1.2.3` as the runtime image, **bypassing any repo-local `<tenant>-devops` chart**. The chosen image is recorded as the env's runtime image, so a later `open` or `deploy --current` reuses it. Once you have built and pushed the env's own image, deploy that version without the flag (or pick the tenant image in the desktop runtime dialog) to switch over. The desktop's version picker offers both the ERun base image and the env's own image and threads this flag for you when you pick the base.

### Naming the chart and the image separately {#runtime-chart-coordinate}

`--version` resolves both coordinates at once, which is right whenever [`erun push`](/cli/push) published the pair. A project that versions its runtime image on **its own** release line breaks that pairing: `team-devops:9.9.9-snapshot-20260101010101` is a real image, but no `erun-devops` chart carries that version and none ever will. `--runtime-chart` states the chart as its own coordinate, so each artifact is named on the line it actually ships on:

```bash
erun deploy team dev \
  --version 9.9.9-snapshot-20260101010101 \
  --runtime-chart oci://ghcr.io/sophium/charts/erun-devops:1.2.3 \
  --runtime-image registry.example/acme/team-devops
```

The chart installs at `1.2.3`; `--version` still stamps the environment's runtime version and tags the image, so the pod runs `registry.example/acme/team-devops:9.9.9-snapshot-20260101010101`. Nothing is inferred -- you name the chart repository and version, and the image repository and version, and deploy uses exactly those.

The reference is a chart repository with an optional `:<version>` suffix. Omit the version and the chart still resolves at `--version`, which is how you point at a different *registry* while keeping the paired version (`--runtime-chart registry.example:5000/charts/erun-devops`; a registry port is not mistaken for a version). An `oci://` scheme is added when you leave it off. The dry-run names the decision, so you can confirm it before rolling:

```
deploy: runtime chart override oci://ghcr.io/sophium/charts/erun-devops version 1.2.3
```

The override applies to the runtime release only; component charts keep resolving at `--version`. It is not persisted -- pass it on each deploy that needs it, so an env's recorded state never implies a chart it was not deployed with.

The desktop resolves this before you commit: picking a version reports which chart it would install, disables Deploy when the registry says there is none, and offers the chart that fixes it — see [Desktop app · Deploying a version](/desktop/deploying-a-version).

For an environment that rides a separately-versioned chart *permanently* -- rather than for one run -- state it once on the environment instead, with [`runtimechart`](/reference/configuration#envconfig). Every later deploy then installs that chart, including one driven from the desktop, which passes only a version. The flag beats the field for a single run and leaves it unchanged, the same way `--runtime-image` relates to `runtimeimage`.

## Flags

| Flag | Description |
|---|---|
| `--version <version>` | The published version to install, by reference. Required unless `--current` is given. The version's image and chart must already exist (locally or in the registry) or the deploy errors — `deploy` never builds them. |
| `--current` | Redeploy the version the environment is already recorded as running (its persisted runtime version). Use it to re-roll the same version, or after a `--force`-style retry, without retyping the number. Required unless `--version` is given. |
| `--runtime-image <ref>` | Install the runtime running this image via the published `erun-devops` chart (`imageOverrides.erun-devops`), pinned to `--version`, **even when the env has a repo-local runtime chart** (which it bypasses). Use it to [bootstrap an env on the canonical ERun base image](#runtime-image-bootstrap) before its own image is built; mirrors [`erun open --runtime-image`](/cli/open). |
| `--runtime-chart <ref>` | Install the runtime from this chart repository, optionally at its own version (`oci://ghcr.io/sophium/charts/erun-devops:1.2.3`). Use it when the chart and the runtime image ship on different release lines, so each is [named on its own line](#runtime-chart-coordinate) instead of both being derived from `--version`. Applies to the runtime release only, and is not persisted. |
| `--components <name,name,...>` | The exact charts to deploy this run — chart directory names under `<tenant>-devops/k8s/`, plus the runtime release name `<tenant>-devops`. Overrides the env's saved selection and the deployment plan for this run; an unknown name (matching no chart and no runtime alias) is rejected with `unknown deploy component`. See [Agent reference · `--components`](/agent-reference/cli-flags#components-value-set). |
| `--mcp-auth-public-key <path>` | Require the environment's [MCP edge](/agent-reference/api-protocol#mcp-edge) to authenticate bearer tokens signed by this PEM public key. The key is recorded on the environment, so later deploys keep authenticating without repeating the flag — see [MCP edge authentication is sticky](#mcp-auth-sticky). |
| `--no-mcp-auth` | Deploy the environment's MCP edge unauthenticated (loopback-only) and forget its recorded public key. Required to turn authentication **off**; deploy refuses to do it by omission. |
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

## MCP edge authentication is sticky {#mcp-auth-sticky}

An environment whose [MCP edge](/agent-reference/api-protocol#mcp-edge) authenticates bearer tokens keeps authenticating across redeploys. `erun deploy` does not reuse the live release's values, so a version bump renders the chart from scratch — and the edge's `raw` tool runs commands in the pod, so an edge that quietly lost its trust anchor is an open door. Deploy closes that by making the setting part of the environment:

- **Enabling records the key where it is applied.** `erun deploy --mcp-auth-public-key <path>` (and [`erun init --mcp-auth-public-key`](/cli/init)) writes the path to the environment's [`mcpauthpublickeypath`](/reference/configuration#envconfig) at the point it hands the key to the cluster, not after the rollout finishes — so a rollout that then fails still leaves the environment naming the key its release trusts, and the redeploy that heals it rethreads that key instead of being refused. The trace names the write: `deploy: mcp auth: recording the public key <path> on <tenant>/<environment>`.
- **A later deploy rethreads it.** With no `--mcp-auth-public-key`, deploy reuses the recorded key and renders the same `mcpAuth.*` values. The trace names the decision: `deploy: mcp auth: rethreading the env's recorded public key <path>`.
- **Turning it off is explicit.** `--no-mcp-auth` resolves no authentication and clears the recorded key — a clear that lands only once the unauthenticated release has actually rolled out.
- **A silent downgrade is refused, and the refusal names the key.** If the live release still has authentication enabled but the resolved plan has none — an environment that enabled it before the key was recorded — deploy stops before the rollout. It reads what the release actually trusts and says so: the desktop identity key on this host (naming the path to pass to `--mcp-auth-public-key`, and its `sha256` so you can see re-supplying keeps the existing trust rather than rotating it), or the `<release>-mcp-auth` Secret and fingerprint when the key is not this host's. `--no-mcp-auth` turns authentication off on purpose in every case.

A hosted environment's runtime deploy Job injects the backend's own MCP-signing public key automatically (no `--mcp-auth-public-key` needed) so the environment's MCP edge trusts tokens the console mints — the same file:// mechanism a desktop deploy uses with its own key, just a different signer.

## Deploying a local-agent environment from inside its own pod {#local-agent-in-pod}

A `local-agent` environment is defined by state that lives on your machine: the checkout the runtime pod hostPath-mounts, the local port range, the pod's resource limits, and the registry its chart comes from. The runtime pod carries only the projection the chart injected, so resolving the deploy in there falls back to defaults — the mount path as the worktree host path, the default port range, default resources, the project's deploy registry — and rolling that out reshapes the environment and cuts the very MCP connection that asked for it.

`erun deploy` refuses that combination: deploying the **runtime** chart of a `local-agent` environment from inside that environment's own pod errors and points at the host CLI. Component-only deploys still work in-pod (a component chart carries no environment shape), and a `remote-agent` environment — which owns its worktree inside the pod — keeps deploying itself normally.

## Moving the worktree onto its own volume {#worktree-adoption}

A `remote-agent` environment keeps its checkout on a volume of its own. Environments created before
that volume existed kept it alongside the rest of the home directory, so the first deploy that brings
the volume in is also the deploy that moves the worktree between volumes.

`erun deploy` neither does that silently nor loses anything doing it. The rollout adopts the existing
checkout onto the new volume before the pod starts, keeps the pre-move copy beside it as
`~/git/<repo>.pre-worktree-volume`, and says so up front:

```
==> Worktree volume team-devops-worktree is not in place yet for team/dev
    /home/erun/git/team is served by that volume, not by team-devops-home
    an existing repository there is adopted onto the new volume before the pod starts, and the pre-move copy is kept at /home/erun/git/team.pre-worktree-volume
    the worktree starts empty when there is nothing to adopt
```

Nothing is printed once the volume is in place — every later deploy leaves the worktree exactly where
it is. If the volume's state can't be read, the notice prints anyway rather than assuming the move
already happened. Once you've confirmed the adopted checkout is what you expect, the
`.pre-worktree-volume` copy is yours to delete.

For the exact adoption rules, see
[Agent reference · Worktree volume adoption](/agent-reference/conventions-spec#worktree-adoption).

## Port-forwards after a rollout

A rollout replaces the runtime pod, and `kubectl port-forward` is bound to one pod: the local socket keeps listening while every request fails. `erun deploy` does not own port-forward lifecycle — [`erun open`](/cli/open) starts and tracks the forwards — so when it finds the environment's tracked MCP forward no longer answering after a rollout, it says so and names the repair:

```
warning: the local port-forwards for team/dev still point at the replaced runtime pod
    re-establish them with: erun open --tenant team --environment dev --no-shell --no-alias-prompt
```

An environment you never opened on this host has no tracked forward, so nothing is reported.

## Skipping helm when nothing changed

When the resolved version matches what the environment already runs and the chart didn't change, `erun deploy` skips the redundant `helm upgrade --install`. Pass `--force` to override.

This means a no-op `erun deploy --current` is essentially free.

## Deploying a stopped environment

`deploy` does not start a [stopped](/cli/stop) environment. It installs a version; whether the
environment should be running is not its decision. So a stopped environment stays stopped through a
deploy — the chart renders `replicas: 0` — and the version you just installed is what starts when
you next [open](/cli/open) it.

This is also the only behaviour that stays consistent with the skip above: a wake-on-deploy would
happen or not depending on whether the helm call ran, which is exactly the kind of surprise you do
not want from a lifecycle change.

## The database reset

A deploy that includes the `erun-backend-postgres` component resets the environment's Postgres database **only when the version being installed is a snapshot** (its version string contains `-snapshot-`). A released version never resets, no matter how many times you redeploy it or which components you include — a snapshot environment's data is disposable by convention, a released one's is not. The dry-run trace names the decision either way (`--set api.postgres.reset=true`/`false` on the postgres chart's helm call).

The reset itself runs as a Helm hook Job, once per deploy of the postgres chart — it is not a step inside the running Postgres pod, so an unplanned pod restart (a crash, an eviction, a node reboot, a `rollout restart`) never re-triggers it, only a deploy does. If a database somehow loses its schema outside of a deploy, `erun-backend-db`'s own repair CronJob re-applies migrations on a short interval so it does not stay schema-less waiting for someone to redeploy.

Reserve snapshot versions for agent and throwaway envs; runtime envs that must keep their data should always deploy a released version.

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

Require the environment's MCP edge to authenticate, recording the key for later deploys:

```bash
erun deploy team dev --version 1.2.3 --mcp-auth-public-key ~/.config/erun/desktopid.pub
```

Turn that authentication back off on purpose:

```bash
erun deploy team dev --version 1.2.3 --no-mcp-auth
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
| The environment is a [host env](/concepts/environment-types#host). | Errors before any change: a host env has no pod and no cluster to deploy against. Exit code 1. |
| Neither `--version` nor `--current` given. | Errors before any change: `deploy requires a version — pass --version <v> or --current`. `deploy` never builds, so there is nothing to install without one. Exit code 1. |
| Cluster unreachable. | Errors before any change; exit code 1, message identifies the context. |
| The live release has MCP authentication enabled but the deploy resolved none. | Errors during resolution, before `helm upgrade`: `MCP auth is enabled on the live <release> release, but this deploy resolved none …`, followed by what that release trusts — the desktop identity key's path on this host, the `<release>-mcp-auth` Secret and its key fingerprint, or (a legacy or hand-configured release) an OIDC issuer, which erun has no supported way to reconfigure. Re-supply the named key with `--mcp-auth-public-key <path>`, or pass `--no-mcp-auth` to turn it off on purpose. See [MCP edge authentication is sticky](#mcp-auth-sticky). Exit code 1. |
| Deploying a `local-agent` environment's runtime chart from inside that environment's own runtime pod. | Errors during resolution, before any change, and names the host command to run instead. The in-pod config is not authoritative for a `local-agent` environment — see [Deploying a local-agent environment from inside its own pod](#local-agent-in-pod). Exit code 1. |
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
