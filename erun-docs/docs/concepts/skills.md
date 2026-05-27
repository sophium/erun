---
title: Skills
---

# Skills

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

ERun's runtime image ships a built-in skill set. The chart mounts them into the env so the Agent (whichever Agent the env is configured for — Claude Code, Codex, …) discovers them automatically when it starts up. The Operator doesn't install or wire anything; opening an env makes the skills available to whatever's running inside.

The built-in set covers the common patterns: HTTP services per language, static-site builds, migration jobs, cron jobs, ingresses. Each is one skill.

Projects can layer their own skills under `<repo>/.erun/skills/`. On `erun open`, those layer on top of the built-ins — same conventions, same shape, just project-specific guidance (your house style, your team's preferred library versions, your auditing rules). A project-side skill with the same name as a built-in wins.

For the SKILL.md contract, the in-pod deployment paths, and the layering rules, see [Agent reference · Skills spec](/agent-reference/skills-spec).

## What the Agent does with a skill

When you ask the Agent something like "add a Go service called `api`," it scans its loaded skills for one whose `description` matches the task. It picks `go-service`, reads the skill's body — the layout, the Dockerfile pattern, the helm chart structure, the deploy-plan rule — and then writes the source + Dockerfile + chart by hand.

The result is files in the conventional places, conformant to the project's layout, *and* sensitive to whatever you described (an HTTP service vs. a gRPC service vs. a background worker — all go-services, all different shapes within the convention).

You can review the diff before it lands. The Agent doesn't run a generator behind your back; everything it writes shows up in your editor's pending changes.

## Where next

- [Conventions](/concepts/conventions) — what the skills teach.
- [Build a small app](/getting-started/build-an-app) — see a skill drive a real build.
- [Agent reference · Skills spec](/agent-reference/skills-spec) — the spec layer.
