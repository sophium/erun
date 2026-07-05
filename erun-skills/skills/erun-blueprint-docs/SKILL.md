---
name: erun-blueprint-docs
description: Scaffold a product documentation site following ERun's blueprint — a Docusaurus 3.x site published to Cloudflare Pages through a Kubernetes Job, the exact shape erun-docs captures — and also maintain, repair, and upgrade an already-scaffolded docs site in place, reconciling it with the current blueprint and re-pinning its versions without clobbering the project's own content pages. Use when the user says "set up product docs site", "scaffold a docusaurus docs site", "build erun-docs-shaped documentation", "create a docs site deployed to cloudflare pages", "add a documentation site for this project", "upgrade the docs site", "repair the docs deploy wiring", "reconcile the docusaurus site with the blueprint", "bump the docs site to <version>", "maintain the docs site", or any similar request for a new or existing project documentation site.
---

# Build an erun-docs-shaped documentation site

Produce a product documentation site following ERun's blueprint — the
same shape `erun-docs` captures: a Docusaurus 3.x static site published
to **Cloudflare Pages** by a two-stage container image run as a
Kubernetes **Job** (a Helm `post-install,post-upgrade` hook), with the
deploy chart living under `erun-devops/k8s/<name>-docs/`.

This skill packages ERun's proven docs-site pattern. Do not freelance
the plumbing; the deploy contract encoded here (build → wrangler image →
hook Job → Cloudflare Pages Direct Upload) is what makes the site ship.

## When to use

Trigger on user phrasings such as:

- "set up product docs site"
- "scaffold a docusaurus docs site"
- "build erun-docs-shaped documentation"
- "create a docs site deployed to cloudflare pages"
- "add a documentation site for this project"

## Context awareness

This skill runs both on a developer laptop and inside a deployed env.
Gate the deploy-plumbing steps (k8s chart, in-cluster Job) on intent,
not on tools — **do not assume `kubectl`/`helm`/`wrangler` are on PATH
on a laptop**. The laptop flow scaffolds the site, runs `yarn build`
locally to prove it, and writes the chart + image files; the actual
in-cluster deploy happens later via `erun deploy` (which the runtime
pod, not the laptop, executes). Only run cluster commands when
`[ -n "${ERUN_TENANT:-}" ]` and the tool exists.

## Inputs to collect

Ask once, then proceed. Do not invent these.

1. **Module name** (e.g. `acme-docs`, `billing-docs`). Directory name;
   default to `<concern>-docs`. Per ERun convention the module is named
   for the concern it documents, suffixed `-docs`.
2. **Target repo root** (default: current working directory). The module
   is a sibling top-level directory, like `erun-docs/`.
3. **Site title + tagline** and the **production URL** (custom domain,
   e.g. `docs.example.com`).
4. **Cloudflare Pages project name** (default: `<module-name>`) and the
   **branch alias** that maps to production (default: `main`).
5. **GitHub org/repo** for the `editUrl` (default: derive from the repo's
   `origin` remote).

## What gets produced

```
<repo-root>/
├── <module-name>/                         # the Docusaurus site
│   ├── docusaurus.config.ts               # 3.x config: url/baseUrl, onBrokenLinks: throw
│   ├── sidebars.ts                        # the doc tree
│   ├── tsconfig.json
│   ├── package.json                       # pinned @docusaurus/* 3.x, yarn
│   ├── docs/                              # MDX/markdown pages
│   ├── src/css/custom.css
│   └── static/img/
└── erun-devops/
    ├── docker/<module-name>/
    │   ├── Dockerfile                      # build site → wrangler image
    │   └── entrypoint.sh                   # wrangler pages deploy $SITE_DIR
    └── k8s/<module-name>/
        ├── Chart.yaml
        ├── values.local.yaml               # agent-env overlay (docs.enabled: false)
        ├── values.prod.yaml                # docs.enabled, project, branch, secret
        └── templates/docs.yaml             # ServiceAccount + hook Job

```

Verbatim-copyable plumbing ships alongside this `SKILL.md` under
`templates/`. Substitute the placeholders (`__MODULE__`, `__PROJECT__`,
`__BRANCH__`, `__TITLE__`, `__TAGLINE__`, `__URL__`, `__ORG__`,
`__REPO__`) and use them as the source of truth — do not freelance the
boilerplate.

## The deploy contract (binding)

This is the load-bearing part of the blueprint. It mirrors
`erun-docs/AGENTS.md` and `erun-devops/k8s/erun-docs/`.

- **Static site, no long-running pod.** The site is built once into a
  container image, then a Job pushes it to Cloudflare Pages. There is no
  in-cluster Service, Ingress, or Deployment. Cloudflare Pages owns CDN,
  TLS, the custom domain, and per-deploy preview URLs.
- **Two-stage image.** Stage 1 (`node:20-alpine`) runs
  `yarn install --frozen-lockfile` then `yarn build` to produce
  `/build`. Stage 2 installs a pinned `wrangler` and bakes the built
  site at `$SITE_DIR=/site`. The entrypoint runs
  `wrangler pages deploy "$SITE_DIR" --project-name="$CF_PAGES_PROJECT" --branch="$CF_PAGES_BRANCH" --commit-dirty=true`.
- **Deploy as a Helm hook Job.** The chart renders a `Job` annotated
  `helm.sh/hook: post-install,post-upgrade` with
  `hook-delete-policy: before-hook-creation,hook-succeeded`. Each
  `erun deploy` runs a fresh Job; success deletes it. `restartPolicy:
  Never`, small `backoffLimit`, `ttlSecondsAfterFinished` so failed Jobs
  self-clean.
- **Credentials via Secret + values.** The Job reads
  `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` from a Secret
  (`credentialsSecretName`, e.g. `cf-creds`) and `CF_PAGES_PROJECT` /
  `CF_PAGES_BRANCH` from chart values. The entrypoint refuses to run if
  any of the four is missing — surface that as the contract, not a
  surprise.
- **Image pinning.** Pin both `node` and `wrangler` versions in the
  Dockerfile so deploys stay reproducible (per repo release rules on
  release-critical infra images).

## External prerequisites (must exist before the first deploy)

These are external to the repository and a deploy will fail without
them. State them to the user up front; do not pretend the scaffold alone
publishes a site.

1. A **Cloudflare Pages project** of type **Direct Upload** named after
   `CF_PAGES_PROJECT`. Do **not** connect a Git source — the Job uploads
   directly via wrangler.
2. The **custom domain** attached to that Pages project.
3. A **Cloudflare API token** scoped `Pages:Edit`, plus the **account
   id**.
4. A Kubernetes **Secret** (e.g. `cf-creds`) in the deploy namespace
   holding `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`.
5. **DNS** for the domain proxied through Cloudflare so TLS is automatic.

## Step-by-step

### Step 1 — confirm inputs

Read back module name, target repo root, title/tagline/URL, Pages
project + branch, and org/repo. Confirm the external prerequisites exist
(or note they must be created before deploy succeeds).

### Step 2 — scaffold the Docusaurus site

Prefer the official generator, then trim to the 3.x classic preset:

```sh
cd "<repo-root>"
npx create-docusaurus@3 "<module-name>" classic --typescript
```

If `npx` / network is unavailable, copy `templates/docusaurus.config.ts`,
`templates/sidebars.ts`, `templates/package.json`, and `templates/tsconfig.json`
and create `docs/intro.md`. Either way, end on the canonical config:

- `url` = production origin, `baseUrl` = `/`.
- `onBrokenLinks: 'throw'` — broken links fail the build (this is the
  link checker; keep it on).
- `organizationName` / `projectName` and `editUrl` pointing at
  `https://github.com/<org>/<repo>/tree/main/<module-name>/`.
- `docs.routeBasePath: '/'`, `blog: false` (docs-only site).

### Step 3 — write the deploy plumbing

Copy and substitute placeholders:

- `templates/Dockerfile` → `erun-devops/docker/<module-name>/Dockerfile`
- `templates/entrypoint.sh` → `erun-devops/docker/<module-name>/entrypoint.sh` (mode 0755)
- `templates/chart/Chart.yaml` → `erun-devops/k8s/<module-name>/Chart.yaml`
- `templates/chart/values.local.yaml` → `erun-devops/k8s/<module-name>/values.local.yaml`
- `templates/chart/values.prod.yaml` → `erun-devops/k8s/<module-name>/values.prod.yaml`
- `templates/chart/templates/docs.yaml` → `erun-devops/k8s/<module-name>/templates/docs.yaml`

Copy a `values.<env>.yaml` for **every** env this chart will be deployed to.
`erun deploy <tenant> <env>` requires a per-chart `values.<env>.yaml` with no
fallback, and the desktop deploys the `<tenant>-local` agent env — so
`values.local.yaml` is required, not optional. Omitting it fails the deploy at
spec resolution: `values file not found for environment "local"`.

### Step 4 — prove the build locally

```sh
cd "<repo-root>/<module-name>"
yarn install --frozen-lockfile
yarn build      # fails on any broken link (onBrokenLinks: throw)
yarn typecheck  # tsc against docusaurus.config.ts and sidebars.ts
```

A clean `yarn build` is the gate. Fix broken links before moving on.

### Step 5 — wire into the deploy flow

Document (in the module's `AGENTS.md`) that the site deploys via
`erun deploy` once the Secret + Pages project exist. Do not run
`kubectl`/`helm` from a laptop; the runtime executes the deploy.

## Audience: keep Operator and Agent docs separate

When authoring pages, split by audience (mirrors `erun-docs/AGENTS.md`):

- **Operator-facing** pages: task-oriented, what a human runs and sees.
- **Agent-reference** pages: exact contracts — flags, inputs, outputs,
  error behaviour, exit codes. Be specific ("aborts with `not in a git
  repository`; exit code 1"), not vague ("errors if not in a repo").

Add every new page id to `sidebars.ts` under the section that matches
its **audience**, not its topic.

## Maintenance, repair & upgrade

This skill owns the site for its whole life: first-time scaffold **and**
ongoing upkeep of what it produced. If the artifacts already exist
(`<module-name>/docusaurus.config.ts`,
`erun-devops/docker/<module-name>/`, `erun-devops/k8s/<module-name>/`),
do **not** stop — enter maintenance mode. It is idempotent and in-place:
safe to re-run, **show the diff/plan before writing**, and touch only
version pins and genuine gaps, never the project's own content pages.

- **Detect.** Probe for the blueprint's artifacts. Any present means
  maintenance, not a fresh scaffold.
- **Repair.** Re-align the site to `erun-docs`'s current blueprint — the
  Docusaurus 3.x classic layout, the Cloudflare-Pages-deploy-via-k8s-Job
  contract, and the chart wiring. Fill gaps against this skill's own
  contract: a missing `values.<env>.yaml` (especially the required
  `values.local.yaml`), a missing `Chart.yaml` / `templates/docs.yaml` /
  `entrypoint.sh`, `onBrokenLinks: 'throw'` turned off, a Git-connected
  Pages project, or drifted deploy plumbing. Repair the deploy wiring
  **without clobbering the operator's `docs/` pages, `sidebars.ts` tree,
  or `src/css/custom.css`** — those are project content, not blueprint.
- **Upgrade.** Two independent version axes — don't conflate them:
  - **erun release axis** — `ERUN_VERSION` in the `Dockerfile` and the
    `Chart.yaml` `version`/`appVersion` track the erun release (the docs
    chart is an erun component chart); re-pin them to the env's
    `runtimeversion` (moved by `erun upgrade`) or an explicit erun target.
  - **docs toolchain axis** — the `node` and `wrangler` tags in the
    `Dockerfile` and the `@docusaurus/*` 3.x pins in `package.json` are the
    site's own toolchain; bump them to current recommended versions,
    independently of the erun release.
  Then refresh derived state: `yarn install` to re-resolve `yarn.lock`,
  `yarn build` to re-prove the site (link checker included), and rebuild the
  image. The chart is a leaf (no `Chart.lock`) unless it later gains
  dependencies, in which case `helm dependency update` regenerates it.
- **Clean up.** Remove only superseded deploy-wiring the blueprint no longer
  emits (a renamed `templates/` file, an obsolete chart file), after previewing.
  Never delete the operator's `docs/` pages, `sidebars.ts`, `src/css/custom.css`,
  or static assets — those are content, not blueprint. A stale Cloudflare Pages
  project or deployment is the operator's to remove, not this skill's.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| Target dir already contains `<module-name>/docusaurus.config.ts` | Do **not** stop — enter maintenance mode (see § "Maintenance, repair & upgrade"): reconcile the deploy wiring against the blueprint and re-pin versions in place, preserving the existing `docs/` content. Do not clobber the operator's pages. |
| `yarn build` fails on a broken link | Fix the link or page id; do not disable `onBrokenLinks`. The throw is the link checker working. |
| `npx create-docusaurus` unavailable (offline) | Fall back to the `templates/` scaffold; the produced files are valid without the generator. |
| Cloudflare Pages project / Secret missing | Scaffold still succeeds; surface that the first `erun deploy` Job will fail until the Direct-Upload project, custom domain, token, and Secret exist. |
| `erun deploy` fails: `values file not found for environment "<env>"` | The chart is missing `values.<env>.yaml`. erun requires one per chart per env with no fallback; the agent env (`<module>`'s `<tenant>-local`) needs `values.local.yaml` too. Create the missing file (an empty/comment-only file is valid). |
| User asks for a Git-connected Pages project | Stop. The Job uploads directly (`wrangler pages deploy`); a Git-connected project double-deploys. Use Direct Upload. |
| PostgreSQL/other backend coupling requested | Out of scope — this skill is the static docs site only. Point at `erun-blueprint-api` / `erun-blueprint-rls-db` for backend modules. |

## Important

- Give the repo root agent guidance. If the repository root has no
  `AGENTS.md`/`CLAUDE.md`, also apply the `erun-blueprint-agents` skill so any
  agent — or human — landing in the repo gets erun-environment orientation.
- Do not connect the Cloudflare Pages project to a Git source. Direct
  Upload + the wrangler Job is the contract; a Git connection causes
  duplicate deploys.
- Do not add a Service/Ingress/Deployment. The site is static and served
  by Cloudflare; the only in-cluster object is the hook Job.
- Do not turn off `onBrokenLinks: 'throw'`. It is the link checker.
- Do not pin docs identity to release metadata indirectly — name the
  module and Pages project explicitly (per repo rule on generated
  runtime asset identity).
- Pin `node` and `wrangler` versions in the Dockerfile; unpinned infra
  images make deploys non-reproducible.
