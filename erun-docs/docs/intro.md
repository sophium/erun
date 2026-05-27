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

## Get started

In your project folder, type:

```bash
erun
```

That's all. The first time, ERun sets things up. After that, the same command picks up where you left off.

---

## Where next

- **[Get hands-on](/getting-started/first-environment)** — a short walkthrough.
- **[Build a small app](/getting-started/build-an-app)** — blank directory to running service in ten minutes.
- **[Three scenarios](/getting-started/three-scenarios)** — peer review, hotfix, CI wait — solved.

ERun isn't a CI/CD replacement, a serverless platform, or a Kubernetes abstraction — it sits alongside those, removing the daily friction. Open source at [github.com/sophium/erun](https://github.com/sophium/erun).
