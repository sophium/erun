---
title: Skills
---

# Skills

ERun ships a small set of **skills** — guidance bundles the Agent loads on demand to do project-shaped work well. You don't install them piece by piece; opening an env gives you the in-pod set, and a single command gives you the same set on your laptop. Then you just describe what you want — the Agent picks the matching skill and writes the code by hand, idiomatic for your project.

For the conceptual model (why skills, not scaffolding) see [Concepts · Skills](/concepts/skills). For the SKILL.md format and the plugin manifest schemas see [Skills spec](/agent-reference/skills-spec).

## Inside a deployed env

Nothing to do. `erun open` brings up an env whose runtime image already has the skills baked in; on env start the entrypoint installs each one into the Agent's discovery directory (`~/.claude/skills/<name>/` for Claude Code, `~/.codex/skills/<name>/` for Codex). The Agent picks them up on its next prompt.

Edits you make to a skill file inside the env survive pod restarts. Only a fresh home (new env, new tenant) re-pulls the baked copy.

## On your laptop

Install the ERun plugin once, and the same skills load into your local Claude Code alongside whatever else you have:

```bash
/plugin marketplace add sophium/erun
/plugin install erun-tools@sophium/erun
```

Skills become invocable as `/erun-tools:<skill-name>`. To update later: `/plugin marketplace update sophium/erun`.

Codex doesn't have an analogous plugin marketplace yet. Inside a deployed env Codex gets the skills automatically; on a laptop, copy the SKILL.md files into `~/.codex/skills/<name>/` manually until upstream Codex ships plugin support.

## What's in the set

| Skill | What it does | Trigger by saying |
|---|---|---|
| `erun-file-issue` | File a bug or feature against ERun on GitHub. | "file an erun bug", "register erun feature", "open an erun issue" |
| `erun-contribute` | Create a new issue against `sophium/erun`, then drive the full clone → branch → implement → PR motion. | "contribute to erun", "submit a PR to erun", "propose an improvement to erun" |
| `erun-scaffold-rls-db` | Generate a multi-tenant PostgreSQL schema with row-level security, Atlas migrations, UUIDv7 keys, and the canonical tenant/issuer/user bootstrap. | "scaffold rls db", "create a multi-tenant postgres schema" |
| `erun-scaffold-api` | Generate a multi-tenant Go HTTP API with OIDC bearer auth, tenant-from-issuer resolution, layered model / repository / routes, transaction-scoped RLS context, and audit logging. | "scaffold multi-tenant api", "create an erun-backend-api-shaped service" |

You don't need to memorise the trigger phrases — the Agent matches against the skill's `description` field, which lists several variants. If you describe the work in plain language ("I want to add a Postgres database that's tenant-scoped"), the right skill usually fires on its own.

## Custom skills

You can layer your own guidance on top — house style, framework preferences, audit rules — by adding a SKILL.md under `<repo>/.erun/skills/<name>/`. (Planned: built-in support; today the runtime image only loads the baked set.) For the format and the layering precedence rules see [Skills spec](/agent-reference/skills-spec).

## Where next

- [Build a small app](/getting-started/build-an-app) — watch the Agent use the in-pod skill set to add a service.
- [Concepts · Skills](/concepts/skills) — the conceptual model.
- [Skills spec](/agent-reference/skills-spec) — SKILL.md format, marketplace manifest, error behaviour.
