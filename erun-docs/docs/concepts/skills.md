---
title: Skills
---

# Skills

> For the Operator workflow — install commands, the v1 catalogue, when to use each one — see [Skills](/collaboration/skills). This page is the conceptual model: why skills instead of scaffolding.

A **skill** is a bundle of guidance the Agent loads into its context when it needs to do something well — write a Go service, set up a migration job, add an Ingress. Skills replace what other platforms call "scaffolding": instead of generating files from a fixed template, ERun teaches the Agent *how* to do the work, and the Agent writes the code itself, idiomatic for your project.

This fits the way agentic coding actually works. The Agent already knows the language. It already knows Kubernetes. It just needs to know **your project's conventions** — where modules live, what the Dockerfile pattern is, how the deploy plan is wired. A skill is that piece of knowledge, delivered into the Agent's context, on demand.

## Why skills, not scaffolding

| Scaffolding (the old model) | Skills (what ERun ships) |
|---|---|
| Generator emits a fixed set of files from a template. | Agent reads guidance, then writes files by hand. |
| Output is uniform — every `go-service` looks the same. | Output is shaped by your description: ports, dependencies, naming, structure all flex. |
| The template is the contract; deviating means editing post-generation. | The skill is the guidance; the Agent applies it situationally. |
| Updating the template is a release. | Updating the skill is a markdown edit. |

The conventions are unchanged — see [Conventions](/concepts/conventions). What changes is *how the Agent honours them*: by reading the skill and writing conformant code, not by invoking a code generator.

## Where skills come from

ERun ships skills through two paths, both vendored from the same canonical source in the ERun repository (`erun-skills/skills/`) so they stay in sync.

### Inside a deployed env

The runtime image bakes the skill set, and the env's entrypoint installs each one into the Agent's discovery directory (`~/.claude/skills/<name>/` for Claude Code, `~/.codex/skills/<name>/` for Codex). The Operator doesn't install or wire anything — opening an env makes the skills available to whatever's running inside. An un-edited skill is refreshed from the image when it changes, so envs track skill improvements across upgrades; edits made to a skill file inside a running env are preserved across pod restarts and rebuilds.

### On your laptop

The same skills are published as a Claude Code plugin via the ERun marketplace at `sophium/erun`. Add the marketplace once, install the plugin, and the skills are loaded into your local Claude Code alongside whatever else you have:

```bash
/plugin marketplace add sophium/erun
/plugin install erun-tools@sophium/erun
```

Codex doesn't have an analogous plugin marketplace yet (Planned.); for now, Codex users either work inside a deployed env or copy the SKILL.md files from `erun-skills/skills/` into `~/.codex/skills/<name>/` manually.

### What's in the set today

Skills come in two kinds: *Blueprint* (ERun's accumulated best practices for industry-strength solutions) and *Workflow* (participate in ERun's processes — report problems, share improvements back).

| Skill | Kind | What it does |
|---|---|---|
| `erun-blueprint-agents` | Blueprint | Give a tenant repo its root agent-guidance file — a canonical `AGENTS.md` plus a `CLAUDE.md` symlink, pre-filled with erun-environment orientation — idempotently, without clobbering hand-authored guidance ([spec](/agent-reference/skills-spec#erun-blueprint-agents)). |
| `erun-blueprint-rls-db` | Blueprint | Build a multi-tenant PostgreSQL schema with row-level security, modelled on `erun-backend-db` — and maintain, repair, or upgrade one it built. |
| `erun-blueprint-api` | Blueprint | Build a multi-tenant Go HTTP API service modelled on `erun-backend-api` — and maintain, repair, or upgrade one it built. |
| `erun-blueprint-service` | Blueprint | Add a service's missing deploy artifacts — a multi-stage Dockerfile and a Helm chart, in the layout `erun build`/`erun deploy` discover by convention — and maintain, repair, or upgrade artifacts it built ([spec](/agent-reference/skills-spec#erun-blueprint-service)). |
| `erun-blueprint-docs` | Blueprint | Scaffold a Docusaurus docs site that publishes to Cloudflare Pages, modelled on `erun-docs` — and maintain, repair, or upgrade one it built. |
| `erun-blueprint-platform` | Blueprint | Lay down the per-env Terraform tree and Helm value overlays that reference erun's published modules and charts, for a tenant that deploys the erun platform itself — and maintain, repair, or upgrade an existing tree in place ([spec](/agent-reference/skills-spec#erun-blueprint-platform)). |
| `erun-enable-hosting-edge` | Workflow | Stand up the public hosting edge — Traefik, cert-manager, and a Cloudflare DNS-01 wildcard-TLS issuer — by applying erun's published Terraform module, and maintain or upgrade it by re-pinning and re-applying ([spec](/agent-reference/skills-spec#erun-enable-hosting-edge)). |
| `erun-file-issue` | Workflow | File a bug or feature against ERun on GitHub (`sophium/erun`). |
| `erun-contribute` | Workflow | Create a new issue against `sophium/erun`, then drive the full clone → branch → implement → PR motion to share your improvement back. |
| `erun-build-env` | Workflow | Extend ERun's published runtime image with your project's own toolchain — and publish a `<tenant>-devops` runtime chart when the tenant ships its own components or needs custom pod shape — then point the environment at the result, and maintain or upgrade it in place ([spec](/agent-reference/skills-spec#erun-build-env)). |
| `erun-browser-session-rest` | Workflow | Call a host's REST API when the org blocks API tokens and gates OAuth, by reusing a saved browser login session ([spec](/agent-reference/skills-spec#erun-browser-session-rest)). |
| `erun-setup-k3s-cluster` | Workflow | Stand up a durable local Kubernetes cluster on Windows for erun to build and deploy to — real k3s inside WSL2 with an in-cluster registry and a WSL-hosted Docker engine (no Docker Desktop) — and wire a `local-agent` environment at it ([spec](/agent-reference/skills-spec#erun-setup-k3s-cluster)). |
| `erun-orchestrate` | Workflow | Act as a host-side orchestrator across agent environments of either type: drive each env through its erun MCP (its raw/build/deploy tools, or its in-pod agent), review each env's host directory read-only — a synced mirror for a `remote-agent` env, the worktree itself for a `local-agent` one — and run built artifacts on this machine, never editing the review directory or reaching into the pod with kubectl ([spec](/agent-reference/skills-spec#erun-orchestrate)). |
| `erun-merge` | Workflow | Take the current branch from "done" to a review sitting at `READY`: resolve the target, merge it in (never rebase), push, open or reuse the review, build and record the result. Stops at `READY`/`FAILED` — never advances the merge queue ([spec](/agent-reference/skills-spec#erun-merge)). |

For the SKILL.md contract, the deployment mechanism, the marketplace manifest format, and the per-skill spec, see [Agent reference · Skills spec](/agent-reference/skills-spec).

## What the Agent does with a skill

When you ask the Agent something like "add a Go service called `api`," it scans its loaded skills for one whose `description` matches the task. It picks `go-service`, reads the skill's body — the layout, the Dockerfile pattern, the helm chart structure, the deploy-plan rule — and then writes the source + Dockerfile + chart by hand.

The result is files in the conventional places, conformant to the project's layout, *and* sensitive to whatever you described (an HTTP service vs. a gRPC service vs. a background worker — all go-services, all different shapes within the convention).

You can review the diff before it lands. The Agent doesn't run a generator behind your back; everything it writes shows up in your editor's pending changes.

## Where next

- [Conventions](/concepts/conventions) — what the skills teach.
- [Build a small app](/getting-started/build-an-app) — see a skill drive a real build.
- [Agent reference · Skills spec](/agent-reference/skills-spec) — the spec layer.
