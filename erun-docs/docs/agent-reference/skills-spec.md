---
title: Skills spec
---

# Skills spec

> For the Operator view, see [Skills](/concepts/skills).

A **skill** is a directory containing a `SKILL.md` plus optional reference content. ERun deploys a built-in set of skills with every env and layers project-specific skills on top. The Agent — Claude Code, Codex, or whatever the env's `aitool` selects — discovers them through its own skill-loading convention.

This page specifies: the on-disk format, the per-tool discovery paths, the deployment mechanism the runtime chart uses, the layering rules, and the built-in skill catalogue.

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

The runtime chart copies skills into the conventional location for the env's configured Agent on env start (the `erun-devops` container's entrypoint). The Agent picks them up automatically — no extra flag or config needed.

| Agent (`EnvConfig.aitool`) | Discovery path inside the runtime pod |
|---|---|
| `claude` | `~/.claude/skills/<skill-name>/SKILL.md` |
| `codex` | `~/.codex/skills/<skill-name>/SKILL.md` |
| Future / generic | `~/.config/agent-skills/<skill-name>/SKILL.md` (canonical ERun path; the entrypoint also symlinks into the per-Agent paths above) |

The canonical ERun path is the source of truth. The per-Agent paths are symlinks the entrypoint creates so each Agent's loader finds them at its expected location.

## Deployment mechanism

Skills are deployed in three layers, applied in this order on every `erun open`:

1. **Built-in skills.** Shipped inside the `erun-devops` runtime image at `/usr/share/erun/skills/<skill-name>/`. Read-only.
2. **Tenant skills.** Mounted from `<projectRoot>/<tenant>-devops/skills/<skill-name>/` (if present) — for project-wide conventions everyone on the team should share. Committed to the repo.
3. **Project / per-env skills.** Mounted from `<projectRoot>/.erun/skills/<skill-name>/` (if present) — for env-specific overrides or experimental skills. Committed to the repo.

The entrypoint copies (not symlinks) each layer into the canonical `~/.config/agent-skills/<skill-name>/` directory in declaration order. Layer N's `SKILL.md` for a given name **overwrites** layer N−1's — the resolved set is what the Agent sees.

Then symlinks fan out from the canonical path to each Agent's expected location.

## Layering rules

| Conflict | Resolution |
|---|---|
| A project skill has the same `name` as a built-in. | Project skill wins. The built-in is hidden in this env. |
| A tenant skill has the same `name` as a built-in. | Tenant skill wins; project skill (if also present) wins over tenant. |
| Two skills in the same layer have the same name. | Sort lexicographically by source path; later wins. (This is a misconfiguration — flag in `erun doctor`.) |
| A skill's `name` frontmatter doesn't match its directory. | The skill is **skipped**; `erun doctor` reports `SKILL_NAME_MISMATCH`. |

## Built-in skill catalogue

The image ships these skills at `/usr/share/erun/skills/`:

| Skill | When the Agent uses it |
|---|---|
| `go-service` | Add a Go HTTP / gRPC / worker service. Produces source module + multi-stage Dockerfile + helm chart + deploy-plan entry. |
| `node-service` | Add a Node service (Express / Fastify / NestJS / similar). |
| `python-service` | Add a Python service (FastAPI / Flask / similar). |
| `java-service` | Add a Java service (Gradle or Maven autodetected). |
| `static-site` | Add a static-site build (Vite / Astro / Next / similar) + Cloudflare Pages / S3 deploy Job. |
| `migration-job` | Add a database migration Job (Atlas / Flyway) wired into the deploy plan via helm hooks. |
| `cron-job` | Add a Kubernetes CronJob with appropriate RBAC. |
| `add-ingress` | Add an Ingress + TLS to an existing component's chart, picking the right hostname pattern by env type. |
| `multi-stage-dockerfile` | Internal sub-skill the language skills cite for the canonical Dockerfile shape. |
| `helm-chart` | Internal sub-skill citing the canonical chart layout. |

The catalogue is open — new skills are added to the runtime image as new patterns emerge. Each skill's `description` is what the Agent matches on, so additions don't require coordinated client changes.

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

A `raw` listing also works: `ls -la ~/.config/agent-skills/`.

## See also

- [Skills](/concepts/skills) — Operator-facing summary.
- [Conventions](/concepts/conventions) — what the skills teach.
- [Conventions spec](/agent-reference/conventions-spec) — the underlying layout the skills target.
