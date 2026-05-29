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

The current v1 set, shipped both in the runtime image (`/etc/erun/skills/`) and via the plugin marketplace:

### `erun-file-issue`

| Field | Value |
|---|---|
| Source | `erun-skills/skills/erun-file-issue/SKILL.md` |
| Description | "Register or file a bug or feature request for the ERun project itself on GitHub." |
| Triggers | "file an erun bug", "file an erun feature", "register erun bug", "register erun feature", "open an erun issue" |
| Inputs | Issue title; what-happened / what-expected / reproduction (or feature goal + acceptance criteria) |
| Outputs | `gh issue create --repo sophium/erun --label bug` (or `--label enhancement`) invocation with a templated body. Body adapts to context: inside an env it includes `${ERUN_TENANT}`, `${ERUN_ENVIRONMENT}`, and the `ERUN_*` env dump; on a laptop it omits those. |
| Error behaviour | `gh` not installed or unauthenticated → surfaces the `gh auth status` hint and stops. Title or body missing → re-prompts the user. |

### `erun-contribute`

| Field | Value |
|---|---|
| Source | `erun-skills/skills/erun-contribute/SKILL.md` |
| Description | "Clone the ERun repository, follow its AGENTS.md rules to implement a change, and submit a pull request back to sophium/erun." |
| Triggers | "contribute to erun", "make a change to erun", "work on erun", "fix erun bug \<id\>", "implement erun feature \<id\>", "land a fix in erun" |
| Inputs | Existing GitHub issue number; issue type (`bug` or `feature`); short kebab-case description for the branch name |
| Outputs | Cloned repo at `~/git/erun`, branch `feature/<n>-…` or `bug/<n>-…`, code change, `make integration-test` run, push, PR via `gh pr create --repo sophium/erun --base main` with `Closes #<n>` in the body |
| Error behaviour | Issue does not exist or is closed → stops and surfaces. `make integration-test` fails → does not push; surfaces the failure. PR title contains an agent marker (`[claude]`, `[codex]`) → re-prompts the user per `AGENTS.md` § "Pull Request Titles". |

Key contract: the skill **explicitly reads** the cloned repo's `AGENTS.md` and every applicable subtree `AGENTS.md` each time it fires. Claude Code does not auto-reload `CLAUDE.md` after a `cd` mid-session, so this read step is binding.

### `erun-scaffold-rls-db`

| Field | Value |
|---|---|
| Source | `erun-skills/skills/erun-scaffold-rls-db/SKILL.md` + `templates/` |
| Description | "Scaffold a multi-tenant PostgreSQL database module with row-level security, Atlas migrations, UUIDv7 surrogate keys, and the canonical ERun tenant/issuer/user bootstrap." |
| Triggers | "scaffold rls db", "scaffold multi-tenant postgres", "create multi-tenant database", "set up rls migrations", "generate an erun-backend-db-shaped module" |
| Inputs | Module name; target directory; list of tenant-owned tables; PostgreSQL major version (default 18) |
| Outputs | `<module>/atlas.hcl`, `<module>/schema/{tables,indexes,triggers,rls,fks}/*.sql`, `<module>/schema/roles.sql`, `<module>/migrations/default/`, `<module>/AGENTS.md`. Bootstrap tables (`tenants`, `tenant_issuers`, `users`, `user_external_ids`) plus one tables/indexes/triggers/rls set per user-supplied table. |
| Error behaviour | Target dir already has `atlas.hcl` → stop, offer `--force` or new path. PostgreSQL \< 18 detected → stop (native `uuidv7()` unavailable). `atlas` not installed → skip validate, surface install hint, continue. User-supplied table name collides with bootstrap names → stop and ask user to rename. |

### `erun-scaffold-api`

| Field | Value |
|---|---|
| Source | `erun-skills/skills/erun-scaffold-api/SKILL.md` + `templates/` |
| Description | "Scaffold a multi-tenant Go HTTP API service modelled on erun-backend-api, with OIDC bearer authentication, tenant resolution from the token issuer, layered model/repository/service/routes structure, transaction-scoped PostgreSQL security context, identity resolution cache, and audit logging." |
| Triggers | "scaffold multi-tenant api", "scaffold backend api", "create an erun-backend-api-shaped service", "generate a Go api with oidc auth and tenant rls" |
| Inputs | Module name; Go module path; target directory; OIDC issuers; initial entities (optional) |
| Outputs | `<module>/go.mod`, `<module>/cmd/<module>/main.go`, `<module>/server.go`, `<module>/auth.go`, `<module>/oidc.go`, `<module>/identity_cache.go`, `<module>/api_path.go`, `<module>/audit.go`, `<module>/internal/{model,repository,routes}/...`, `<module>/AGENTS.md`. Includes a working `GET /v1/whoami` endpoint; entity routes are scaffolded per user-supplied entity. |
| Error behaviour | Target dir already has `go.mod` → stop. Empty OIDC issuer list → stop. Database side (matching `erun-backend-db`-shaped schema) missing → surface and offer to run `erun-scaffold-rls-db` first. `go build` fails after generation → surface compiler output; most common cause is module path mismatch. |

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
