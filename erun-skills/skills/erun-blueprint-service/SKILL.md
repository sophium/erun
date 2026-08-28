---
name: erun-blueprint-service
description: Add a custom service's deploy artifacts — a multi-stage Dockerfile, a Helm chart, and per-env values overlays — in the exact layout `erun build` and `erun deploy` discover by convention (`<tenant>-devops/docker/<component>/`, `<tenant>-devops/k8s/<component>/`), so a hand-written or generated service becomes a component erun can build and ship without anyone reverse-engineering the convention. Also maintains, repairs, and upgrades deploy artifacts it previously produced in place, without clobbering the service's own source or hand-authored chart templates. Use when the user says "add deploy artifacts for this service", "scaffold a Dockerfile and helm chart for <component>", "make this service deployable with erun", "wire up build and deploy for <component>", "add a component chart", "this service has no Dockerfile/chart yet", "upgrade the <component> chart", "repair the <component> deploy wiring", "reconcile <component>'s deploy artifacts", or any similar request to give an existing or new service the Dockerfile/chart/values layout erun's build and deploy commands need.
---

# Add a component's deploy artifacts

Produce the deploy artifacts erun needs to build and ship a component — a
multi-stage Dockerfile, a Helm chart (`Chart.yaml` + `templates/`), and a
`values.<env>.yaml` overlay per environment — in the exact layout
`erun build`/`erun push`/`erun deploy` discover by convention, per
`erun-docs/docs/agent-reference/conventions-spec.md` § "Component naming":

```
<projectRoot>/<tenant>-devops/docker/<component>/Dockerfile
<projectRoot>/<tenant>-devops/k8s/<component>/Chart.yaml
<projectRoot>/<tenant>-devops/k8s/<component>/templates/service.yaml
<projectRoot>/<tenant>-devops/k8s/<component>/values.<env>.yaml
```

This skill owns the **deploy artifacts only** — the Dockerfile, chart, and
values overlays. It does not write the service's own source code. Run it
against a service that already exists (hand-written, or produced by another
blueprint skill such as `erun-blueprint-api`) to give it the missing last
mile: the artifacts `componentHelmChartCandidate`/`findComponentHelmChartPath`
(`erun-common/deploy.go`) actually discover, so `erun build`/`erun deploy
<component>` work with no further wiring.

This skill packages ERun's convention for a deployable component. Do not
freelance the layout; the discovery contract encoded here — literal
component-named directories, literal `Chart.yaml`/`Deployment`/`Service`
names — is what makes `erun build`/`erun deploy` find the artifacts at all.

## When to use

Trigger on user phrasings such as:

- "add deploy artifacts for this service"
- "scaffold a Dockerfile and helm chart for `<component>`"
- "make this service deployable with erun"
- "wire up build and deploy for `<component>`"
- "add a component chart"
- "this service has no Dockerfile/chart yet"

## Context awareness

Runs both on a developer laptop and inside a deployed env. The artifacts are
repo files — write them to the git worktree, never to `${ERUN_OUTPUTS_DIR}`.
Validation (`helm lint`/`helm template`) needs `helm` on `PATH`; when it
isn't, skip validation and say so rather than failing the whole skill —
`erun-devops`'s own runtime image ships `helm`, so this only matters on a
laptop with no local install. Do not run `erun build`/`erun deploy` yourself
unless the user asks; scaffolding the artifacts is this skill's job, running
them is the operator's.

## Inputs to collect

Ask once, then proceed. Do not invent these.

1. **Tenant** — the project name (the directory holding `.git`); default to
   the repo root's basename.
2. **Component name** — must match `^[a-z][a-z0-9-]*$` (lowercase letters,
   digits, hyphens; first char a letter — the same DNS-1123 constraint the
   name lands in as a K8s resource name). **Prefix it with the tenant**
   (`acme-widget`, not bare `widget`) unless the user explicitly declines —
   see "Why the tenant prefix matters" below; this is not cosmetic, it is
   what makes the chart's literal resource naming line up with what
   `erun expose` targets.
3. **Source location** — where the component's own code already lives (or
   will live), so the Dockerfile's `COPY`/build commands point at the right
   path. If no source exists yet, ask whether to point at
   `erun-blueprint-api` (or another service-authoring skill) first, or
   whether the operator will fill in the Dockerfile's build stage by hand.
4. **Language/toolchain** — to pick the Dockerfile builder stage's base
   image and build/test commands. The shipped template defaults to Go; note
   in the plan that the operator swaps the builder stage for their toolchain
   if it's something else (the runtime stage's shape — thin base, non-root
   user, `ENTRYPOINT` — stays the same regardless of language).
5. **Container port** and **health-check path** (default `8080` /
   `/healthz`) — used for the Service port and the Deployment's
   readiness/liveness probes.
6. **Environments to generate values overlays for.** `values.local.yaml` is
   always required (the desktop deploys `<tenant>-local`); ask which runtime
   env(s) (e.g. `prod`) also need one.
7. **Publicly reachable?** If yes, no extra chart wiring is needed — see
   "Making it reachable" below; just confirm the component name already
   carries the tenant prefix (step 2), since that is what determines the
   Service name `erun expose` will target.

## Check for an ambiguous match before writing (binding)

Before writing anything, check whether a Helm chart for this component name
already resolves **anywhere** in the project tree, not just under the target
`<tenant>-devops/k8s/`. `componentHelmChartCandidate`
(`erun-common/deploy.go`) matches *any* `*/k8s/<component>/Chart.yaml`, so a
second chart directory with the same component name — even in an unrelated
`k8s/` folder elsewhere in the repo — makes every future `erun deploy
<component>` fail with `multiple Helm charts found for component
"<component>"` the moment this scaffold lands, not before.

```sh
find "<repo-root>" -type f -path '*/k8s/*/Chart.yaml' \
  -path "*/k8s/<component>/Chart.yaml"
```

- **No match** → proceed.
- **One match at the target path** (`<tenant>-devops/k8s/<component>/`) →
  that's this skill's own prior output; enter maintenance mode (below), not
  a fresh scaffold.
- **One match elsewhere** → stop and tell the user: this component name
  already resolves to a different chart; picking this name would make
  `erun deploy <component>` ambiguous. Rename, or point at the existing
  chart instead of writing a second one.

## What gets produced

```
<repo-root>/
└── <tenant>-devops/
    ├── docker/
    │   └── <component>/
    │       └── Dockerfile              # multi-stage: builder -> thin runtime
    └── k8s/
        └── <component>/
            ├── Chart.yaml
            ├── values.local.yaml        # required — the agent env deploys this
            ├── values.<env>.yaml        # one per requested runtime env
            └── templates/
                └── service.yaml         # Deployment + Service, literal names
```

Verbatim-copyable plumbing ships alongside this `SKILL.md` under
`templates/`. Substitute the placeholders (`__COMPONENT__`, `__PORT__`,
`__HEALTH_PATH__`, `__ENV__`) and use them as the source of truth — do not
freelance the boilerplate.

## The deploy contract (binding)

This mirrors `erun-docs/docs/agent-reference/conventions-spec.md` §
"Component naming" and § "Multi-stage Dockerfile expectation" exactly —
those are the resolution algorithms `erun build`/`erun deploy` run against
these paths.

- **The component name is the single key.** It appears identically as the
  Docker build-context directory name, the Helm chart directory name, the
  `Chart.yaml` `name:`, the image tag (`<registry>/<component>:<version>`),
  and — in the chart this skill writes — the literal `Deployment`/`Service`
  `metadata.name` and the pod label `app: <component>`.
- **Why the tenant prefix matters.** Unlike erun's own published, many-tenant
  platform component charts (`erun-backend-api` and friends, which scope
  every resource name via `.Values.tenant` because one chart is redeployed
  under many different tenants — see `erun-devops/AGENTS.md`), this chart is
  authored once for one tenant's own component and never redeployed under a
  different tenant. So the chart names its `Deployment`/`Service` **literally**
  `<component>` — no `.Values.tenant` templating. That literal name is what
  `componentHelmChartCandidate` expects **and**, once the component name
  itself carries the tenant prefix (`acme-widget`), it is already the
  `<tenant>-<service>` shape `erun expose` targets (see "Making it reachable"
  below). Skipping the prefix breaks the second property, not the first —
  the chart still deploys, but exposing it later needs a rename.
- **Multi-stage Dockerfile.** A builder stage provisions the toolchain, runs
  the tests (`RUN go test ./...` or the toolchain's equivalent — a failing
  test fails the build, so no image is produced from red tests), and
  produces the artefact; a thin runtime stage ships only that artefact,
  non-root. `templates/Dockerfile` is the Go-flavoured skeleton; swap the
  builder stage's base image and commands for another toolchain.
- **`values.<env>.yaml` is required per env, no fallback.** `erun deploy
  <tenant> <env>` reads it from this chart directory with no base
  `values.yaml` fallback and no config-dir overlay. Missing one fails the
  whole deploy at spec resolution:
  `values file not found for environment "<env>": …/<component>/values.<env>.yaml`.
  `values.local.yaml` is not optional — the desktop composes
  build→push→deploy for `<tenant>-local` on every create and via the Deploy
  button.
- **Image resolution follows the shared convention.** The chart defaults the
  image to `<containerRegistry>/<component>:<Chart.AppVersion>` and lets
  `imageOverrides.<component>` win when set — the same mechanism
  `erun deploy` already threads for every other component
  (`--set-string imageOverrides.<component>=<image>`); no extra wiring
  needed for that path.
- **`erun push` re-stamps `Chart.yaml` at publish time.** `version`/
  `appVersion` in the shipped `Chart.yaml` template are placeholders
  (`0.1.0`); `overrideHelmChartVersion` (`erun-common/deploy.go`) rewrites
  both to the resolved build version whenever `erun push` packages this
  chart, so there is nothing to hand-maintain there.

## Making it reachable

- **In-cluster only (the default):** nothing further to do. Other components
  in the same namespace reach it at
  `<component>.<tenant>-<env>.svc.cluster.local`.
- **Publicly reachable, on a platform deployment:** run
  `erun expose <tenant> <env> <service>` (see
  `erun-docs/docs/cli/expose.md`), where `<service>` is the tenant-stripped
  role part of the component name (e.g. `widget` for component
  `acme-widget`). It applies an Ingress routing the public hostname to the
  in-namespace Service `<tenant>-<service>` — which, because this skill
  named the component with the tenant prefix and rendered the Service
  literally as `<component>`, is exactly the Service this chart already
  produced. A mismatch here — a component name without the tenant prefix,
  or a hand-edited Service name — produces a correct-looking Ingress that
  routes to a Service that doesn't exist: a 503, not an error at `expose`
  time. Don't add an Ingress inside this chart itself; `erun expose` owns
  that for platform deployments.

## Step-by-step

### Step 1 — confirm inputs

Read back tenant, component name (and confirm the tenant-prefix), source
location, language/toolchain, port, health path, and target envs.

### Step 2 — check for an ambiguous match

Run the check above. Stop and report if the name collides elsewhere.

### Step 3 — write the Dockerfile

Copy `templates/Dockerfile` to
`<tenant>-devops/docker/<component>/Dockerfile`, substituting
`__COMPONENT__` and `__PORT__`. Adapt the builder stage to the actual
toolchain if it isn't Go — keep the test-then-build shape and the thin,
non-root runtime stage.

### Step 4 — write the chart

Copy and substitute placeholders:

- `templates/chart/Chart.yaml` → `<tenant>-devops/k8s/<component>/Chart.yaml`
- `templates/chart/templates/service.yaml` → `<tenant>-devops/k8s/<component>/templates/service.yaml`
- `templates/chart/values.local.yaml` → `<tenant>-devops/k8s/<component>/values.local.yaml`
- `templates/chart/values.env.yaml` → `<tenant>-devops/k8s/<component>/values.<env>.yaml`, once per requested runtime env

### Step 5 — validate

```sh
helm lint "<tenant>-devops/k8s/<component>"
helm template "<tenant>-devops/k8s/<component>" \
  --set tenant=<tenant> --set environment=local \
  -f "<tenant>-devops/k8s/<component>/values.local.yaml"
```

Confirm the rendered output has exactly one `Deployment` and one `Service`,
both named `<component>`, and that the Service's `targetPort` matches the
container's `containerPort`. If `helm` isn't on `PATH`, say so and skip —
don't fail the whole skill over a missing local tool.

### Step 6 — optional: register the deploy-plan order

If the project's `.erun/config.yaml` has an
`environments.<env>.k8s.deployments` plan (it's optional — an absent plan
falls back to a default rank that still deploys an unlisted component, just
last), offer to append the component to it for the envs it targets, so its
ordering relative to other components is explicit rather than implicit.
Show the diff and let the operator confirm; never reorder existing entries.

## Maintenance, repair & upgrade

This skill owns the artifacts for their whole life, not just day one. If
`<tenant>-devops/docker/<component>/Dockerfile` or
`<tenant>-devops/k8s/<component>/Chart.yaml` already exists, do **not**
stop — enter maintenance mode. Idempotent and in-place: safe to re-run, show
the diff/plan before writing, and touch only the deploy-wiring gap or drift —
never the service's own source, the Dockerfile's builder-stage toolchain
layers, or a hand-added chart template.

- **Detect.** Either artifact present at the conventional path means
  maintain, not scaffold.
- **Repair.** Fill gaps against this skill's contract: a missing
  `values.<env>.yaml` (especially `values.local.yaml`), a `Deployment`/
  `Service` that isn't literally named `<component>`, a missing readiness/
  liveness probe, or a runtime stage that isn't thin/non-root. Repair
  without touching the Dockerfile's builder-stage toolchain commands or any
  chart template this skill didn't generate.
- **Upgrade.** This is the component's **own** release line, independent of
  the erun version — there is no erun-version coupling to re-pin (unlike
  `erun-build-env`'s runtime-image `FROM`). "Upgrade" means re-aligning
  structural drift against this skill's current template shape (see
  Repair) and re-running `helm lint`/`helm template` to confirm.
- **Clean up.** Remove only chart/Dockerfile scaffolding this skill
  previously emitted but no longer does (e.g. a renamed template file),
  after previewing. Never delete the service's own source or a hand-authored
  chart template.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| Component name fails `^[a-z][a-z0-9-]*$` | Ask the user to rename; this is the DNS-1123 constraint the name lands in as a K8s resource name (`INVALID_COMPONENT_NAME`). |
| Component name collides with an existing chart elsewhere in the tree | Stop before writing (see "Check for an ambiguous match"); ask the user to rename or reuse the existing chart. |
| Component name is the reserved `<tenant>-devops` | Refuse — that name is the runtime-pod chart's, owned by `erun-build-env`. |
| Dockerfile/chart already exist at the conventional path | Not a stop — enter maintenance mode (see above): reconcile gaps in place, preview first, never clobber the service's own code or hand-authored templates. |
| `erun deploy` fails: `values file not found for environment "<env>"` | The chart is missing `values.<env>.yaml`. Create it (a comment-only file is valid), remembering `values.local.yaml` is required too. |
| `erun deploy <component>` fails: `multiple Helm charts found for component "<component>"` | A second `*/k8s/<component>/Chart.yaml` exists somewhere in the tree — the ambiguous-match case this skill checks for up front slipped through (e.g. added by hand after scaffolding). Rename one of the two. |
| `helm lint`/`helm template` unavailable (no local `helm`) | Skip validation and say so; it isn't installed by default on a laptop. The runtime image ships it, so validation still runs in a deployed env. |
| `erun expose` reaches the component but the Ingress 503s | The Service `erun expose` targets (`<tenant>-<service>`) doesn't match what this chart rendered — usually because the component name wasn't tenant-prefixed at scaffold time. Rename the component (and its chart/Docker directories) to carry the prefix, or pass the exact post-prefix role as `<service>` to `erun expose`. |
| User wants a one-shot Job (migration, cron) instead of a long-running service | Out of scope for this skill's `templates/chart/templates/service.yaml` shape — point at `erun-docs/docs/agent-reference/conventions-spec.md` § "Helm Job pattern for one-shots" and hand-write the Job chart; the Dockerfile/directory-naming half of this skill still applies. |

## Important

- Give the repo root agent guidance. If the repository root has no
  `AGENTS.md`/`CLAUDE.md`, also apply the `erun-blueprint-agents` skill so any
  agent — or human — landing in the repo gets erun-environment orientation.
- This skill writes deploy artifacts only. It never writes the service's own
  application code — that's the operator's, or another blueprint skill's
  (e.g. `erun-blueprint-api`), job.
- Never template the `Deployment`/`Service` name by tenant inside the chart.
  This chart is one tenant's own component, not a published multi-tenant
  chart; literal naming is both the discovery contract and (with the tenant
  prefix on the component name) the `erun expose` contract.
- Never add an Ingress inside this chart. Public reachability on a platform
  deployment goes through `erun expose`, which owns the Ingress and the
  per-env wildcard DNS record.
- Do not skip the ambiguous-match check. A second same-named chart elsewhere
  in the tree is a silent time bomb — it deploys fine today and breaks
  `erun deploy <component>` the next time someone runs it.
- Keep the Dockerfile multi-stage (builder → thin runtime), even when
  adapting it to a different toolchain than the Go example. Single-stage
  Dockerfiles aren't rejected by erun, but lose the cache/size/security
  benefits the convention exists for.
