---
title: Introduction
slug: /intro
---

# ERun

**Frictionless agentic coding.**

<figure className="erun-hero-figure">
  <img src="/img/operator-plus-erun.svg" alt="An anatomy diagram of ERun. At the top, three interface pills — Desktop app, Terminal (`erun` command), AI agents (via MCP tools) — all feed into a single wide charcoal box labelled ERun, with the subtitle 'spawns, manages, tears down isolated environments'. From ERun, three arrows fan out to a row of three environment cards labelled env: feature, env: review, env: hotfix. Inside each environment, an Operator pill and an Agent pill sit side by side, with the note 'side by side, full stack inside (api · db · queue · ui)'. A '+ more' indicator to the right shows unlimited capacity. The bottom strapline reads: 'Operator and Agent work side by side in every environment — your machine, the cloud, or both.'" />
  <figcaption>ERun is the control plane behind parallel development. Three ways to drive it (desktop app, terminal, AI agent), and a fleet of isolated environments — in each one, you and an Agent work side by side.</figcaption>
</figure>

Brooks's law still applies — nine Agents can't deliver tomorrow's design today. But for the work that *can* be split up (different features, different bug fixes, different services), one Operator plus ERun ships what used to take a team.

## The problem

Even before AI agents, operators struggled to run more than one copy of their dev stack on the same machine. Port conflicts, shared databases, daemons fighting over the host — switching branches meant tearing the world down. Everyone hits this:

- **Peer review lands mid-feature.** Their branch needs your stack — but your stack is busy.
- **Emergency hotfix while you're building.** Tear the env down, stand up a clean one, fix, deploy, dance back. Hours on plumbing, not code.
- **Integration tests serialize on CI.** Your laptop only has one stack, so the PR queues behind the build server. Fail → fix → push → wait.

`git worktree` solves the *file* side. ERun solves the *runtime* side — every environment is its own Kubernetes namespace with the full stack inside.

<figure className="erun-hero-figure">
  <img src="/img/problem-vs-erun.svg" alt="Side-by-side comparison. Left half labelled 'Without ERun': three task pills (feature, peer review, hotfix) all funnel into a single 'Your dev stack' box; the feature arrow is active and cyan, the peer-review and hotfix arrows are dashed grey and labelled 'queued'. The stack shows 'in use: feature, queued: 2'. Right half labelled 'With ERun': the same three tasks each connect by an active cyan arrow into their own dedicated namespace (ns: feature, ns: peer-review, ns: hotfix), each marked 'full stack inside'." />
  <figcaption>One stack = one task at a time. A namespace per task = all in parallel.</figcaption>
</figure>

## Under the hood

Kubernetes is the right primitive for production, but exposed raw it's overwhelming. ERun gives the Agent and your IDE everything they need to work in an env without dealing with the cluster directly. **The Agent drives Kubernetes; you drive the Agent.**

<figure className="erun-hero-figure">
  <img src="/img/abstraction-stack.svg" alt="The ERun abstraction stack. Operator directs an Agent (Claude Code, Codex). A dashed 'loads' arrow brings skill bundles deployed into the env into the Agent's context. The Agent uses two channels into the env's runtime pod: ssh for file authoring and shell, mcp for typed ERun operations. The Operator's IDE (VS Code, IntelliJ, Cursor, Zed, …) attaches over the same ssh channel — Agent and IDE see the same files. The runtime pod lives in a Kubernetes namespace alongside the env's application services (api, db, queue, ui); other envs live in their own namespaces in the same cluster." />
</figure>

---

## What makes ERun different

Four properties that don't usually come together, and the reason ERun feels different from everything else.

<figure className="erun-hero-figure">
  <img src="/img/differentiators.svg" alt="Four cards explaining what makes ERun different. Side by side — your editor and your Agent see the same project, no parallel worlds. In control — preview every action before it runs, every action recorded, join, take over, hand off any time. Industry standards — traceable, reproducible, audit-ready, compliance is how the platform works, not a bolt-on. Your scale — LAMP stack on a VM through autoscaling enterprise with DR and immutable backups, same defaults at every scale." />
</figure>

Each one is a chapter elsewhere in these docs. The point of putting them on one page is that they reinforce each other — none of them works as a bolt-on to a platform built without the others.

---

## AI native

ERun is built for an Operator working with an Agent, not for an Operator working alone. Every env ships **skills** — guidance bundles the Agent loads when you describe intent, then the Agent writes the code by hand, idiomatic for your project. You don't run a code generator and you don't pick a template; you describe what you want, the matching skill teaches the Agent the layout and conventions, and the diff lands in your editor for review.

```mermaid
flowchart LR
    O("Operator describes intent"):::endpoint --> A("Agent picks matching skill"):::step
    A --> P("Platform · erun-file-issue"):::step
    A --> W("Shared workflows · erun-contribute"):::step
    A --> B("Blueprint · erun-blueprint-rls-db · erun-blueprint-api"):::step
    P --> R("Agent writes code, idiomatic for your project"):::endpoint
    W --> R
    B --> R

    classDef endpoint fill:#0f1320,color:#ffffff,stroke:#0a1019,stroke-width:1px,rx:14,ry:14;
    classDef step     fill:#ffffff,color:#0f1320,stroke:#0891b2,stroke-width:1.5px,rx:14,ry:14;
```

**Three categories ship today.** *Platform* skills interact with the ERun platform itself — `erun-file-issue` files a bug or feature against `sophium/erun` on GitHub. *Shared workflow* skills let ERun users share their best practices and workflows back to the platform so other users benefit — `erun-contribute` drives the full create-issue → clone → branch → implement → PR motion for an improvement you want to land in ERun. *Blueprint* skills package ERun's accumulated best practices for building complex industry-strength solutions — `erun-blueprint-rls-db` produces a multi-tenant PostgreSQL schema with row-level security, Atlas migrations, UUIDv7 keys, and the canonical tenant/issuer/user bootstrap; `erun-blueprint-api` produces a multi-tenant Go HTTP API with OIDC bearer auth, tenant-from-issuer resolution, layered model/repository/routes, and transaction-scoped RLS context.

The same skills load in two places: inside any env you open (automatic — `erun open` bakes them in), and on your laptop Claude Code via the ERun plugin (`/plugin marketplace add sophium/erun`). User-authored skills layered on top — your house style, your framework preferences, your audit rules — are (Planned.).

For the full catalogue, the trigger phrases, and the install commands see [Skills](/collaboration/skills); for the conceptual model see [Concepts · Skills](/concepts/skills); for the SKILL.md format and the marketplace manifest schemas see [Skills spec](/agent-reference/skills-spec).

---

## Where next

- **[Get hands-on](/getting-started/first-environment)** — a short walkthrough.
- **[Build a small app](/getting-started/build-an-app)** — blank directory to running service in ten minutes.
- **[Three scenarios](/getting-started/three-scenarios)** — peer review, hotfix, CI wait — solved.

ERun isn't a CI/CD replacement, a serverless platform, or a Kubernetes abstraction — it sits alongside those, removing the daily friction. Open source at [github.com/sophium/erun](https://github.com/sophium/erun).
