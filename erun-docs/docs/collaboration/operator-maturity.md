---
title: Operator maturity
---

# Operator maturity

How an Operator works with Agents evolves through three levels. Each level removes friction; the last unlocks **integration testing as part of every feature build** — without that, multi-Agent work hits a wall the moment you try to test seriously.

<figure className="erun-hero-figure">
  <img src="/img/operator-maturity.svg" alt="Three operator maturity levels stacked vertically. Level 1: Operator prompts a Chat (browser) and copies code into the Solution. Level 2: Operator directs Claude Code inside the dev environment which commits straight to the Solution. Level 3: Operator orchestrates three Agents in three isolated environments, all converging on the Solution." />
</figure>

## Level 1 — Copy-paste from chat

The starting point. The Operator opens a chat tool in a browser (ChatGPT, Claude, Gemini), describes a task, gets code back, copies it into their editor.

What's good: low setup. Anyone can do it on day one.

What's painful:
- The Agent has no view into the project beyond what the Operator pasted in.
- Every iteration is a manual round-trip — copy out, paste in, copy back, paste back.
- No tests against a running system. Code is "looks right to me" until merged.
- One task at a time.

## Level 2 — Agent in your dev environment

The Agent (Claude Code, Codex, similar) runs **inside the same environment as the Operator**. Same files, same shell, same build tools. The Operator directs; the Agent edits files, runs commands, commits.

What's better:
- No copy-paste. Diffs land in the workspace directly.
- The agent can `ls`, `cat`, `git diff`, `grep` — actual project state, not a paste.
- The Operator and the Agent watch the same terminal output.

What's still limiting:
- One environment, one agent. Two agents in the same env step on each other's files.
- The environment is whatever the Operator has locally — may not match production.
- Integration testing means tearing down what you were doing and rebuilding the world; expensive, so usually skipped.

## Level 3 — Many Agents in many environments

The Operator orchestrates **multiple Agents, each in its own ERun environment**. Every agent gets a full Kubernetes namespace — backend, database, queues, everything the feature needs to run. The Operator joins any env to review, take over, or hand back.

What this unlocks:
- **Parallel work without crosstalk.** Three agents working on three features don't see each other's files, services, or test runs.
- **Real integration tests on every build.** Each env is a full functioning copy of the system; the Agent can run end-to-end tests as part of the build loop, not as a separate "later" phase.
- **One person, team-scale throughput** (within Brooks's Law — parallel features parallelize, sequential design does not).

## Why integration tests are the inflection point

Integration tests are the only honest test of a feature: does it actually work end-to-end, against a real database, a real queue, real services? Unit tests can pass while integration is broken.

But integration tests need a **full environment** to run. If multiple Agents share one env, every test run risks racing another agent's test setup. If the env is half-built or polluted with leftover state, tests lie. The only sane way to run integration tests as part of the build loop is to give each agent its own fully-isolated environment.

That's exactly what ERun's per-env Kubernetes namespace provides:

- Each agent's env has its own database, queues, deployed services — built from the same charts as production.
- Integration tests against the env are routine: spin up, run, tear down (or keep around for debugging).
- The Operator can join any agent's env to watch a failing test fire and reproduce it interactively.

Without isolated environments per agent, Level 3 collapses back to Level 2 — only one agent can do anything serious at a time, because tests can't run in parallel.

## How ERun makes Level 3 reachable

The hard part of Level 3 isn't the Agent — it's the DevOps. Standing up an isolated environment with the full stack (Kubernetes namespace, database, queues, services, build cache, image registry, secrets, network policy) per Agent is a project of its own. Most teams don't have the slack to build it, so they stay at Level 2.

ERun solves this by **making complex DevOps approachable**. It does it three ways:

- **Automated by convention.** Project layout, build flow, deploy plan, runtime image, helm chart — all follow conventions ERun knows (see [Conventions](/concepts/conventions)). No bespoke wiring per project. Spinning up a fresh environment is one command.
- **Agents drive it themselves via MCP.** Every environment exposes a [Model Context Protocol](/mcp/overview) server with typed tools: `idle`, `doctor`, `list`, `version`, plus `raw` for arbitrary shell access. An agent can build, push, deploy, inspect logs, run tests — directly, without the Operator typing commands. The platform is callable by code, not just by humans.
- **Agents can scale agents.** When a task naturally parallelizes, an Agent can call `erun init` for a new environment via MCP, kick off a peer agent to work in it, and tear the env down when the work merges. The Operator decides *what* to scale; the platform makes scaling actually cheap.

This is the unlock. The Operator's role shifts from "I do DevOps so the Agent can code" to "I set direction; the Agent and the platform handle the rest."

## What moves you between levels

Going from one level to the next isn't about a better AI model — it's about removing infrastructure friction.

| From → To | What you need |
|---|---|
| 1 → 2 | An Agent that runs in your terminal / IDE and has filesystem + shell access (Claude Code, Codex, …). |
| 2 → 3 | A platform that gives every agent its own isolated environment cheaply — including a database, services, the full stack — and lets agents drive that platform themselves. (This is what ERun does.) |

The first transition is free — just install Claude Code. The second is what most teams stall on: standing up isolated environments per agent is hard without infrastructure for it. ERun's job is to make that transition routine.
