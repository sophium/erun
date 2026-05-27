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

Kubernetes is the right primitive for production, but exposed raw it's overwhelming. The Agent and the Operator's IDE reach an env through two channels on the runtime pod: **SSH** for file authoring and shell — the same channel your editor attaches to — and [**MCP**](/mcp/overview) for typed ERun operations the Agent calls instead of hand-running `kubectl` / `helm`. ERun also deploys [**skills**](/concepts/skills) into the env so the Agent picks up your project's conventions automatically. **The Agent drives Kubernetes; you drive the Agent.**

<figure className="erun-hero-figure">
  <img src="/img/abstraction-stack.svg" alt="Abstraction stack from top to bottom. Operator pill at the top, with a 'directs' arrow down to the Agent box (Claude Code, Codex). A dashed 'loads' arrow enters the Agent box from a side panel labelled 'skills · deployed into the env'. A 'calls' arrow goes down from the Agent to the MCP tools layer holding six typed actions: init, deploy, doctor, list, build, raw. A 'drives' arrow down to the Kubernetes cluster, which contains three namespace cards labelled ns: feature-a, ns: feature-b, ns: feature-c — each marked as a full stack inside (api · db · queue · ui)." />
  <figcaption>The MCP layer is what abstracts Kubernetes — file authoring rides a parallel SSH channel into the same runtime pod (the channel your IDE attaches to, and the one the Agent uses for edits and shell).</figcaption>
</figure>

---

## What makes ERun different

**Side by side with your tools.** Your usual editor (VS Code, IntelliJ, Cursor) and your usual Agent (Claude Code, Codex) see the same project at the same time. No parallel worlds.

**In control.** Preview every action before it runs. Every action recorded. Join any environment, take over, or hand off — any time.

**Built to industry standards.** Traceable, reproducible, audit-ready. Compliance is how the platform works, not a layer added at the end.

**Meets you where you are.** From a classic LAMP stack on a single VM to autoscaling enterprise with DR and immutable backups — same defaults, scaled to whatever you bring.

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
