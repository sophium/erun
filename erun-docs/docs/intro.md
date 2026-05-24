---
title: Welcome
slug: /intro
---

# ERun

**ERun is built so a person — or a whole organization — with an idea can ship it at agentic-coding pace, on production-grade infrastructure, without compromising on compliance or industry best practices.**

The primary aim is to enable **agentic coding**: AI agents that don't just write code but actually build, deploy, diagnose, and iterate. The constraint is that the result still has to pass a real audit — immutable release tags, reproducible builds, per-environment isolation, auditable command traces.

In other words: rapid, agent-driven iteration on one end; industrial-grade software shipped at the other end; nothing weakened in between.

## The three commitments

1. **Agent-first surface.** Every environment exposes a structured MCP server. Every action supports `--dry-run` whose trace lines match the real run. Every module ships an `AGENTS.md` capturing the rules that apply to it. Agents and humans read the same contracts.
2. **Iteration speed.** Snapshot tags for local iteration. Stable release tags for promotion. Fingerprint cache so fresh clones promote pinned bases without rebuilding. Idle-stop so cloud compute doesn't bill you while you sleep. One-command workflows from `init` to `deploy`.
3. **Compliance preserved by default.** Immutable release tags. Per-environment Kubernetes isolation. Auditable dry-run traces for every action. Cloud contexts that bind to specific accounts, regions, and IAM roles. Multi-architecture builds verified at developer-machine time, not at remote deploy time. None of these are opt-in.

## What you can do with ERun

- Spin up an isolated runtime pod for any project with one command (`erun init` + `erun open`).
- Let an AI agent drive your dev loop — through MCP, with structured tools and traceable actions.
- Iterate locally with snapshot builds that are safe to overwrite and instant to rebuild.
- Promote the same code to a production-grade environment with stable, immutable release tags — no rebuild, no mutation, no surprises.
- Switch between local development and managed cloud clusters without changing your workflow.
- Stop paying for idle cloud compute automatically when you walk away.

## Where to start

- [Why ERun](/why) — the design principles in detail, including the agentic-coding affordances and the compliance contract.
- [Install ERun](/getting-started/install).
- [Create your first environment](/getting-started/first-environment).
- [Concepts: tenants and environments](/concepts/tenants-and-environments).

## Project

ERun is open source under [github.com/sophium/erun](https://github.com/sophium/erun).
