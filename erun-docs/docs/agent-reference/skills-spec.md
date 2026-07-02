---
title: Skills spec
---

# Skills spec

> For the Operator view, see [Skills](/concepts/skills).

A **skill** is a directory containing a `SKILL.md` plus optional reference content. ERun owns one canonical source of skills (`erun-skills/skills/<name>/` in the `sophium/erun` repository) and publishes them through two paths: baked into the runtime image for in-pod use, and via a Claude Code plugin marketplace for laptop use. The Agent — Claude Code, Codex, or whatever the env's `aitool` selects — discovers them through its own skill-loading convention.

This page specifies: the on-disk format, the per-tool discovery paths, the deployment mechanism the runtime chart uses, the marketplace distribution contract, the layering rules, and the built-in skill catalogue.

## SKILL.md format

A skill bundle is a directory:

```
<skill-name>/
├── SKILL.md           # required; the entrypoint the Agent reads
├── references/        # optional; longer-form reference content the SKILL.md cites
│   └── ...
├── examples/          # optional; worked examples the Agent can use as starting points
│   └── ...
└── scripts/           # optional; helper scripts the Agent can invoke as part of applying the skill
    └── ...
```

`SKILL.md` has YAML frontmatter followed by a markdown body:

```markdown
---
name: go-service
description: Write a conformant Go HTTP service with the ERun multi-stage Dockerfile and helm chart layout. Use when the Operator asks to add a Go service, gRPC service, or background worker written in Go.
---

# Go service

When you're adding a Go service to an ERun project, follow this layout.

## Source module

Live at `<projectRoot>/<name>/`. Layout:
…

## Dockerfile

Live at `<projectRoot>/<tenant>-devops/docker/<name>/Dockerfile`. Multi-stage…

…
```

| Field | Required | Validation |
|---|---|---|
| `name` | yes | Matches `^[a-z][a-z0-9-]*$`. Must equal the parent directory name. Uniquely identifies the skill. |
| `description` | yes | One sentence; under 200 characters. This is what the Agent reads to decide whether the skill applies — phrase it as **when** to use the skill, not **what** the skill contains. |

The body is plain markdown — the same content the Agent would read as instructions. Sections, lists, code blocks, links to reference files all work.

## Per-tool discovery paths

The `erun-devops` container's entrypoint copies each skill into the conventional location for the env's configured Agent on every env start. The Agent picks them up automatically — no extra flag or config needed.

| Agent | Discovery path inside the runtime pod |
|---|---|
| `claude` | `~/.claude/skills/<skill-name>/SKILL.md` |
| `codex` | `~/.codex/skills/<skill-name>/SKILL.md` |

Both paths are installed in parallel from the same `/etc/erun/skills/<name>/` source baked into the image. A generic canonical path under `~/.config/agent-skills/` is (Planned.) for future tools.

## Source module

Skills live in exactly one place in the source tree: `erun-skills/skills/<skill-name>/` in `sophium/erun`. Both the runtime image and the plugin marketplace vendor from this directory. Editing a skill is a single-file change in that module; the two distribution paths pick it up automatically.

Module guidance: [`erun-skills/AGENTS.md`](https://github.com/sophium/erun/blob/main/erun-skills/AGENTS.md).

## Deployment mechanism

Two paths deliver the skill set to the Agent:

### In-pod (runtime image)

The runtime Dockerfile vendors the whole tree with one line:

```dockerfile
COPY --chmod=0644 erun-skills/skills /etc/erun/skills
```

On every entrypoint run, `initialize_claude_config` and `initialize_codex_config` iterate every subdirectory under `/etc/erun/skills/` and install each skill into `~/.claude/skills/<name>/` and `~/.codex/skills/<name>/` — but only when the destination `SKILL.md` is absent. Supporting files (templates, helper scripts) inside the skill directory ship with the skill automatically.

The `[ ! -e ]` guard means a user can edit `~/.claude/skills/<name>/SKILL.md` inside a running env and the edit survives pod restarts. Only a fresh home (or a new skill name) re-pulls the baked copy.

### Laptop (plugin marketplace)

The repo-root file `.claude-plugin/marketplace.json` publishes `erun-skills/` as the `erun-tools` plugin via a `git-subdir` source. Users add the marketplace and install:

```bash
/plugin marketplace add sophium/erun
/plugin install erun-tools@sophium/erun
```

Skills become invocable as `/erun-tools:<skill-name>` (e.g. `/erun-tools:erun-file-issue`). See [Marketplace distribution](#marketplace) below for the full schema and update flow.

### Layering (Planned.)

Tenant-level skills (`<projectRoot>/<tenant>-devops/skills/<name>/`) and project-level skills (`<projectRoot>/.erun/skills/<name>/`) are reserved as future layers above the in-pod baked set. The current implementation ships only the runtime-image baked set; tenant and project layers are not yet mounted.

## Marketplace distribution {#marketplace}

The plugin is published from `sophium/erun` itself — the repo *is* the marketplace. No second repository to maintain.

### `.claude-plugin/marketplace.json`

At the repo root. Schema (current, as published):

```json
{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "sophium/erun",
  "description": "ERun skills for Claude Code …",
  "owner": { "name": "Sophium", "email": "maintainers@sophium.com" },
  "plugins": [
    {
      "name": "erun-tools",
      "description": "…",
      "category": "development",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/sophium/erun.git",
        "path": "erun-skills",
        "ref": "main",
        "sha": "<commit-sha>"
      },
      "homepage": "https://github.com/sophium/erun/tree/main/erun-skills"
    }
  ]
}
```

Fields:

| Field | Required | Notes |
|---|---|---|
| `name` (marketplace) | yes | `sophium/erun`. Used by `/plugin marketplace add` and as the suffix in `/plugin install <plugin>@<marketplace>`. |
| `owner` | recommended | Display info for the catalogue UI. |
| `plugins[].name` | yes | `erun-tools`. Used as the namespace prefix for skill invocation (`/erun-tools:<skill>`). |
| `plugins[].source.source` | yes | `git-subdir` so the plugin can live in a subdirectory of the marketplace repo. |
| `plugins[].source.url` | yes | HTTPS git URL — must be cloneable without credentials for public marketplaces. |
| `plugins[].source.path` | yes | `erun-skills`. The plugin root inside the marketplace repo. |
| `plugins[].source.ref` | yes | Branch (`main`) used to resolve the SHA on update. |
| `plugins[].source.sha` | yes | Pinned commit hash. Users only see updates when this changes. |
| `plugins[].homepage` | recommended | Browseable URL for the plugin source. |

### `erun-skills/.claude-plugin/plugin.json`

The plugin manifest. Skills are auto-discovered from `erun-skills/skills/`; they do not need to be enumerated.

```json
{
  "name": "erun-tools",
  "version": "1.0.0",
  "description": "…",
  "author": { "name": "Sophium", "email": "maintainers@sophium.com" },
  "homepage": "https://github.com/sophium/erun/tree/main/erun-skills",
  "repository": "https://github.com/sophium/erun",
  "license": "MIT"
}
```

### Update flow

1. Edit a skill in `erun-skills/skills/<name>/SKILL.md`.
2. Commit and merge to `main`.
3. The release flow bumps `source.sha` in `.claude-plugin/marketplace.json` to the merge commit. (Until release automation handles this end-to-end, the bump lands in the same PR as the skill edit.)
4. Users see the update on next `/plugin marketplace update sophium/erun`.

Auto-update for third-party marketplaces is disabled by default in Claude Code; users either opt in via the `/plugin` UI or run `/plugin marketplace update` manually.

### Install commands

```bash
/plugin marketplace add sophium/erun                            # add once
/plugin install erun-tools@sophium/erun                         # install plugin
/reload-plugins                                                 # pick up mid-session
/plugin marketplace update sophium/erun                         # refresh catalogue
/plugin uninstall erun-tools@sophium/erun                       # remove
```

### Error behaviour

| Failure | What the user sees | Recovery |
|---|---|---|
| Marketplace repo unreachable (network, auth) | `/plugin marketplace add` fails with the upstream git clone error. | Check network / `gh auth status`; retry. |
| `marketplace.json` malformed JSON | `/plugin marketplace add` fails with the parse error and the offending line. | File an issue against `sophium/erun`. |
| `source.sha` no longer reachable (history rewritten) | Install fails with a "commit not found" error. | Re-run `/plugin marketplace update sophium/erun` to fetch the latest SHA. |
| Skill name collision with an existing user-installed skill of the same name | Claude Code namespaces the plugin-shipped skill as `/erun-tools:<name>`, so collisions are not possible at the invocation layer. | n/a. |
| Plugin install succeeds but no skills appear | `/reload-plugins` may be needed. If still missing, the plugin manifest may have rejected the install — check `/plugin` UI for an error entry. | Run `/plugin` and inspect the **Errors** tab. |

### Codex distribution (Planned.)

Codex CLI does not have an analogous plugin marketplace yet. Inside a deployed env, Codex receives the same skills via the runtime-image baked install — no extra step. For laptop Codex use, copy `erun-skills/skills/<name>/SKILL.md` into `~/.codex/skills/<name>/` manually until upstream Codex ships plugin support.

## Layering rules

| Conflict | Resolution |
|---|---|
| A project skill has the same `name` as a built-in. | Project skill wins. The built-in is hidden in this env. |
| A tenant skill has the same `name` as a built-in. | Tenant skill wins; project skill (if also present) wins over tenant. |
| Two skills in the same layer have the same name. | Sort lexicographically by source path; later wins. (This is a misconfiguration — flag in `erun doctor`.) |
| A skill's `name` frontmatter doesn't match its directory. | The skill is **skipped**; `erun doctor` reports `SKILL_NAME_MISMATCH`. |

## Built-in skill catalogue

The current v1 set, shipped both in the runtime image (`/etc/erun/skills/`) and via the plugin marketplace. Skills come in two semantic kinds:

- **Blueprint skills** — package ERun's accumulated best practices for building complex industry-strength solutions.
- **Workflow skills** — let users participate in ERun's processes (report problems, share improvements back so other users benefit).

### `erun-file-issue`

| Field | Value |
|---|---|
| Kind | Workflow — participate in ERun's issue-reporting process. |
| Source | `erun-skills/skills/erun-file-issue/SKILL.md` |
| Description | "Register or file a bug or feature request for the ERun project itself on GitHub." |
| Triggers | "file an erun bug", "file an erun feature", "register erun bug", "register erun feature", "open an erun issue" |
| Inputs | Issue title; what-happened / what-expected / reproduction (or feature goal + acceptance criteria) |
| Outputs | `gh issue create --repo sophium/erun --label bug` (or `--label enhancement`) invocation with a templated body. Body adapts to context: inside an env it includes `${ERUN_TENANT}`, `${ERUN_ENVIRONMENT}`, and the `ERUN_*` env dump; on a laptop it omits those. |
| Error behaviour | `gh` not installed or unauthenticated → surfaces the `gh auth status` hint and stops. Title or body missing → re-prompts the user. |

### `erun-contribute`

| Field | Value |
|---|---|
| Kind | Workflow — lets users share improvements back to the platform so other users benefit. |
| Source | `erun-skills/skills/erun-contribute/SKILL.md` |
| Description | "Contribute a change to the ERun platform itself — create a new GitHub issue against sophium/erun that captures the work, clone the repo, implement the change following its AGENTS.md rules, and submit a pull request back." |
| Triggers | "contribute to erun", "make a change to erun", "work on erun", "land a fix in erun", "submit a PR to erun", "propose an improvement to erun" |
| Inputs | One- or two-sentence description of the change; sentence-style title; issue type (`bug` / `feature` / `enhancement`); short kebab-case description for the branch name (defaulted from the title) |
| Outputs | A newly-filed issue against `sophium/erun` (Step 1), then a cloned repo at `~/git/erun`, branch `feature/<n>-…` or `bug/<n>-…`, code change, `make integration-test` run, push, PR via `gh pr create --repo sophium/erun --base main` with `Closes #<n>` in the body |
| Error behaviour | `gh issue create` fails (auth, network, label not allowed) → stops; does not proceed to clone without an issue number to anchor the PR. `make integration-test` fails → does not push; surfaces the failure. PR title contains an agent marker (`[claude]`, `[codex]`) → re-prompts the user per `AGENTS.md` § "Pull Request Titles". |

Semantic: `erun-contribute` is **initiator-driven** — the same person who runs it both files the issue and ships the PR. For reporting a problem without intent to follow up, use `erun-file-issue` instead. For picking up an issue someone else filed, no skill applies; the user clones, branches, implements, and PRs directly.

Key contract: the skill **explicitly reads** the cloned repo's `AGENTS.md` and every applicable subtree `AGENTS.md` each time it fires. Claude Code does not auto-reload `CLAUDE.md` after a `cd` mid-session, so this read step is binding.

### `erun-blueprint-rls-db`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's accumulated best practices for multi-tenant PostgreSQL. |
| Source | `erun-skills/skills/erun-blueprint-rls-db/SKILL.md` + `templates/` |
| Description | "Build a multi-tenant PostgreSQL database module following ERun's blueprint — row-level security, Atlas migrations, UUIDv7 surrogate keys, shared timestamp trigger, separate erun_tenant / erun_operations PostgreSQL roles, and the canonical tenant/issuer/user bootstrap that erun-backend-db captures." |
| Triggers | "build a multi-tenant postgres database", "create a tenant-scoped postgres schema with row-level security", "set up multi-tenant postgres migrations", "I need an erun-backend-db-shaped module", "build a multi-tenant rls db" |
| Inputs | Module name; target directory; list of tenant-owned tables; PostgreSQL major version (default 18) |
| Outputs | `<module>/atlas.hcl`, `<module>/schema/{tables,indexes,triggers,rls,fks}/*.sql`, `<module>/schema/roles.sql`, `<module>/migrations/default/`, `<module>/AGENTS.md`. Bootstrap tables (`tenants`, `tenant_issuers`, `users`, `user_external_ids`) plus one tables/indexes/triggers/rls set per user-supplied table. |
| Error behaviour | Target dir already has `atlas.hcl` → stop, offer `--force` or new path. PostgreSQL \< 18 detected → stop (native `uuidv7()` unavailable). `atlas` not installed → skip validate, surface install hint, continue. User-supplied table name collides with bootstrap names → stop and ask user to rename. |

### `erun-blueprint-api`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's accumulated best practices for multi-tenant HTTP APIs. |
| Source | `erun-skills/skills/erun-blueprint-api/SKILL.md` + `templates/` |
| Description | "Build a multi-tenant Go HTTP API service following ERun's blueprint — OIDC bearer authentication, tenant resolution from the token issuer, layered model / repository / service / routes structure, transaction-scoped PostgreSQL security context, identity resolution cache, and audit logging. Captures the patterns that erun-backend-api packages." |
| Triggers | "build a multi-tenant http api", "build a multi-tenant backend api", "create an erun-backend-api-shaped service", "I need a multi-tenant Go api with oidc auth and tenant rls" |
| Inputs | Module name; Go module path; target directory; OIDC issuers; initial entities (optional) |
| Outputs | `<module>/go.mod`, `<module>/cmd/<module>/main.go`, `<module>/server.go`, `<module>/auth.go`, `<module>/oidc.go`, `<module>/identity_cache.go`, `<module>/api_path.go`, `<module>/audit.go`, `<module>/internal/{model,repository,routes}/...`, `<module>/AGENTS.md`. Includes a working `GET /v1/whoami` endpoint; entity routes are produced per user-supplied entity. |
| Error behaviour | Target dir already has `go.mod` → stop. Empty OIDC issuer list → stop. Database side (matching `erun-backend-db`-shaped schema) missing → surface and offer to run `erun-blueprint-rls-db` first. `go build` fails after generation → surface compiler output; most common cause is module path mismatch. |

### `erun-blueprint-docs`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's docs-site pattern: a Docusaurus 3.x site published to Cloudflare Pages by a Kubernetes hook Job, the shape `erun-docs` captures. |
| Source | `erun-skills/skills/erun-blueprint-docs/SKILL.md` + `templates/` |
| Description | "Scaffold a product documentation site following ERun's blueprint — a Docusaurus 3.x site published to Cloudflare Pages through a Kubernetes Job, the exact shape erun-docs captures." |
| Triggers | "set up product docs site", "scaffold a docusaurus docs site", "build erun-docs-shaped documentation", "create a docs site deployed to cloudflare pages", "add a documentation site for this project" |
| Inputs | Module name (default `<concern>-docs`); target repo root; site title + tagline + production URL; Cloudflare Pages project name + branch alias; GitHub org/repo for `editUrl` |
| Outputs | `<module>/` Docusaurus site (`docusaurus.config.ts` with `onBrokenLinks: throw`, `sidebars.ts`, `docs/`, `src/css`, `static/img`, `package.json`, `tsconfig.json`); `erun-devops/docker/<module>/{Dockerfile,entrypoint.sh}` (two-stage build → pinned wrangler); `erun-devops/k8s/<module>/{Chart.yaml,values.local.yaml,values.prod.yaml,templates/docs.yaml}` (ServiceAccount + `post-install,post-upgrade` hook Job that runs `wrangler pages deploy`). Both `values.local.yaml` (agent env, `docs.enabled: false`) and `values.prod.yaml` ship, because `erun deploy` requires a per-chart `values.<env>.yaml` for every env — including the `<tenant>-local` agent env the desktop deploys — with no fallback. |
| Error behaviour | Target dir already has `<module>/docusaurus.config.ts` → stop. `yarn build` fails on a broken link → fix the link, do not disable `onBrokenLinks`. `npx create-docusaurus` offline → fall back to bundled `templates/`. Cloudflare Pages project / `cf-creds` Secret missing → scaffold still succeeds; surface that the first `erun deploy` Job fails until the Direct-Upload project, custom domain, token, and Secret exist. User asks for a Git-connected Pages project → stop (Direct Upload only; a Git connection double-deploys). `erun deploy` fails `values file not found for environment "<env>"` → the chart is missing `values.<env>.yaml`; create it (an empty/comment-only file is valid), remembering the agent env needs `values.local.yaml`. |

### `erun-blueprint-platform`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's accumulated best practices for hosted-platform deploy wiring. |
| Source | `erun-skills/skills/erun-blueprint-platform/SKILL.md` |
| Description | "Blueprint the deploy artifacts for a hosted erun platform — a per-env Terraform tree (terraform-\<tenant\>/) whose modules wrap erun's published Terraform modules, and the per-env Helm values overlays plus thin umbrella charts that reference erun's published OCI charts — all version-pinned to the erun release the environment runs." |
| Triggers | "blueprint the platform", "scaffold the platform terraform", "set up the platform helm charts and terraform", "create the terraform-\<tenant\> structure", "blueprint erun platform deploy", "set up platform deploy artifacts" |
| Inputs | The env's tenant + short env name; the erun version to pin to (`erun version` in-pod, or the env's `runtimeversion`); the platform values (`base_domain`, `services_zone`, `acme_email`); the container registry (env `containerregistry`, default `ghcr.io/sophium`). |
| Outputs | `terraform-<tenant>/{common.tf, variables.tf, .gitignore}` (canonical providers + shared vars), `terraform-<tenant>/modules/terraform-<tenant>-cluster-edge/` (wraps erun's `terraform-erun-cluster-edge` by `?ref=v<version>`), and per env a `terraform-<tenant>/<env>/` folder whose `common.tf`/`variables.tf` are **symlinks** to the root and that adds the env's services via its own `main.tf` + `<env>.tfvars`; plus, per platform component, a thin umbrella `<tenant>-devops/k8s/<tenant>-<component>/Chart.yaml` (directory name, chart `name:`, and Helm release all `<tenant>-<component>`, e.g. `frs-docs`) depending on erun's published `erun-<component>` OCI chart, with a **per-chart `values.<env>.yaml` for every env it deploys to — including `values.local.yaml`**. `erun deploy <tenant> <env>` reads `<tenant>-<component>/values.<env>.yaml` from each chart dir (required, no fallback, no config-dir overlay) and keys the component name off the directory, and the desktop deploys the `<tenant>-local` agent env, so a missing `values.local.yaml` fails the deploy. Each umbrella's resolved dependency is tracked as `Chart.lock` (committed) with `charts/*.tgz` gitignored (`**/charts/*.tgz`); `erun deploy` runs `helm dependency build` before install, rebuilding `charts/` from `Chart.lock`, so the tgz is never committed (vendor it only for an air-gapped install). No `run.tf`, no per-env shell scripts — [`erun terraform apply`](/cli/terraform) owns the apply workflow. |
| Error behaviour | `terraform-<tenant>/` already exists → stop, offer to add a new `<env>/` folder. erun version unresolvable → stop and ask (never default to `main` for production wiring). `?ref=v<version>` doesn't resolve on `terraform init` → pin to a released `vX.Y.Z`. `helm dependency build` 404s → that version's chart isn't published; pin to a pushed version. `erun deploy` fails `values file not found for environment "<env>"` → the umbrella chart lacks `values.<env>.yaml`; create it (empty/comment-only is valid), including `values.local.yaml` for the agent env. `erun deploy` fails `tenant is required` (or `environment is required`) → the wrapped subchart reads those in its own scope and `erun deploy` sets them only top-level; forward them nested under the dependency name in the umbrella's `values.<env>.yaml`. A component that can't run in an env (`erun-powerdns` needs `:53`/hostNetwork + a private-image pull secret) → omit it from that env's `--components`, don't force it. Operator asks to put the Cloudflare token in `<env>.tfvars` → refuse; it is injected as `TF_VAR_cloudflare_api_token` at apply time. |

### `erun-build-env`

| Field | Value |
|---|---|
| Kind | Workflow — extend the environment's runtime image through ERun's supported extension path. |
| Source | `erun-skills/skills/erun-build-env/SKILL.md` |
| Description | "Create a custom build environment by extending ERun's published runtime image with the project's own toolchain, then pointing the environment at the result." |
| Triggers | "init build environment", "init erun build environment", "create a custom build environment", "customize the runtime image" |
| Surfaced by | `erun build` — and the build it runs for `--release` / `--deploy`, plus the MCP `build` tool — prints a one-line advisory recommending this skill whenever it runs in a project with no `<tenant>-devops` build module (the module that would hold the custom runtime image). The advisory fires regardless of whether the build itself succeeds. |
| Inputs | The tooling to add (packages, toolchains, CLIs); the target tenant + environment. The module and image names are fixed by convention: `<tenant>-devops` for both (see Outputs). |
| Outputs | A `<tenant>-devops` module (outer directory name **must end in `-devops`** — `erun build` discovers the runtime build module by that suffix) containing a starter Dockerfile at `<tenant>-devops/docker/<tenant>-devops/Dockerfile` (inner directory name becomes the image name) with `FROM <registry>/erun-devops:<runtime-version>` (version read from `erun version` in-pod, or the env's `runtimeversion` / `erun list` on a laptop); a `VERSION` file at the module root (`<tenant>-devops/VERSION`, e.g. `1.0.0`) — `erun build` mints the image version from it; an `erun build` run that builds both architectures and pushes to the env's registry; the env's [`runtimeimage`](/reference/configuration#envconfig) field set to `<tenant>-devops` via `erun init --runtime-image <ref>` or a direct config edit. On the next deploy/open the image rides into the published chart as `imageOverrides.erun-devops` ([Advanced chart values](/reference/configuration#advanced-chart-values)). |
| Error behaviour | Runtime version unresolvable (no `runtimeversion` in the env config and not inside a pod) → asks the Operator before writing the Dockerfile. Dockerfile target path already exists → stops and asks before overwriting. Module directory not ending in `-devops`, or no `VERSION` file at the module root → `erun build` fails (`dockerfile not found in current directory` / `version file not found for current module`); the skill's Steps 2–3 produce the layout that avoids both. `erun build` fails (e.g. `BINFMT_MISSING`, registry push rejected) → surfaces the build error and does not touch the env config. Base image other than `erun-devops` requested → refuses; the entrypoint, the Agent tooling, and the in-pod `erun` live in that image. |

### `erun-browser-session-rest`

| Field | Value |
|---|---|
| Kind | Workflow — authenticated REST against a host that blocks API tokens, via a reused browser session. |
| Source | `erun-skills/skills/erun-browser-session-rest/SKILL.md` + `save-session.mjs` + `request.mjs` |
| Description | "Make authenticated REST calls to a host whose org blocks API tokens and admin-gates OAuth, by reusing a saved browser login session (Playwright storageState)." |
| Triggers | "authenticated REST via a browser session", "call an API that blocks API tokens", "reuse my browser login for API calls", "hit the `<host>` API without a token" |
| Inputs | Host base URL (`ERUN_REST_BASE_URL` / `--base`); login URL (`ERUN_REST_LOGIN_URL` / `--login`); session-file path (`ERUN_REST_SESSION` / `--session`, default `./session.json`); per call: HTTP method, path, optional JSON body. No host, credentials, or IdP are baked in. |
| Outputs | `save-session.mjs` opens a real browser for manual login (SSO + MFA) and writes a Playwright `storageState` session file (cookies only — never a password). `request.mjs` makes the authenticated call, prints the response body to stdout, and rolls the session forward (re-saves it) so refreshed cookies persist. |
| Error behaviour | Missing base/login URL → usage error, exit 2. Session file missing/unreadable → "run save-session.mjs first", exit 2. HTTP error status → response body to stdout, status to stderr, exit 1. Expired session (401 / login redirect) → re-run `save-session.mjs`. Requires Node 18+ and Playwright (`npx playwright install chromium`). |
| Security | The session file holds live session cookies — treat it as a secret, keep it out of git, keep it short-lived. The login is intentionally manual; the skill never stores a plaintext password. This is a fallback — prefer an API token or an approved OAuth app whenever the host allows one. |

### `erun-enable-hosting-edge`

| Field | Value |
|---|---|
| Kind | Workflow — applies the public hosting edge to a cluster through ERun's published Terraform module. |
| Source | `erun-skills/skills/erun-enable-hosting-edge/SKILL.md` |
| Description | "Stand up the public hosting edge for an erun cluster — a Traefik ingress controller, cert-manager, and a Cloudflare DNS-01 ClusterIssuer that issues wildcard TLS for the services zone — by applying the terraform-erun-cluster-edge module." |
| Triggers | "enable the hosting edge", "enable public hosting", "set up TLS ingress for erun", "apply the cluster edge", "set up cert-manager and traefik", "issue wildcard TLS for the services zone" |
| Inputs | The env's `CLOUDFLARE_API_TOKEN` (injected by a Cloudflare alias) passed as `TF_VAR_cloudflare_api_token`; the services zone (`platform.serviceszone`) and ACME email (`platform.acmeemail`); the erun version the module `?ref` pins to (`erun version`, else `main` off-pod). |
| Outputs | A Terraform root (in a temp dir) that references `terraform-erun-cluster-edge` from erun's GitHub by `?ref=v<version>` and applies it: a Traefik ingress controller, cert-manager + CRDs, a Cloudflare DNS-01 `ClusterIssuer` (`erun-cloudflare`), and a wildcard `Certificate` for `*.<services-zone>`. Idempotent — re-running reconciles. |
| Error behaviour | No `CLOUDFLARE_API_TOKEN` → stops, points at `erun cloud init cloudflare` + `erun cloud set … --alias <name>@cloudflare`. `terraform`/`kubectl` missing, or `kubectl` not pointed at a reachable cluster → stops. ClusterIssuer/Certificate stalls → `kubectl describe` the ACME order/challenge; usual causes are a token missing `Zone:Read`+`DNS:Edit` or the services zone not yet delegated to Cloudflare. While validating a fresh zone, `-var acme_server=<staging>` avoids Let's Encrypt production rate limits. |

### Catalogue evolution

The catalogue is open — new skills land in `erun-skills/skills/` and ship through both distribution paths automatically. Each skill's `description` is what the Agent matches on, so additions don't require coordinated client changes.

## Adding a custom skill

1. Create `<projectRoot>/.erun/skills/<skill-name>/SKILL.md` with the frontmatter above plus your guidance body.
2. Commit it. Anyone who opens an env on this project picks it up on the next `erun open`.
3. `erun doctor` validates skill bundles on startup — frontmatter parse failures, name mismatches, and missing `SKILL.md` show up as `skill.<name>` check failures.

## Inspecting deployed skills

The MCP `doctor` tool reports the resolved skill set per env:

```jsonc
{
  "checks": [
    {
      "name": "skills",
      "status": "ok",
      "detail": "10 built-in + 2 project skills loaded",
      "skills": [
        { "name": "go-service",  "source": "builtin"  },
        { "name": "house-style", "source": "project"  }
      ]
    }
  ]
}
```

A `raw` listing also works: `ls -la /etc/erun/skills/ ~/.claude/skills/ ~/.codex/skills/`.

## See also

- [Skills](/concepts/skills) — Operator-facing summary.
- [Marketplace distribution](#marketplace) — the plugin manifest and update flow.
- [Conventions](/concepts/conventions) — what the skills teach.
- [Conventions spec](/agent-reference/conventions-spec) — the underlying layout the skills target.
