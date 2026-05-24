---
title: Welcome
slug: /intro
---

# ERun

**ERun closes the gap between a developer who wants to build something and that thing running on production-grade infrastructure.**

The hard problems between "I have an idea" and "real users are using it" — reproducible multi-architecture builds, Kubernetes deployments, container registries, release tagging, environment isolation, cost-controlled cloud compute, AI tooling integration — are not what most developers signed up to spend their week on. ERun takes care of them by default, and exposes the result as a tiny CLI and desktop app that gets out of your way.

The goals, in order:

1. **Developer-first.** The interface is one CLI command at a time. No YAML to write, no helm values to author, no kubeconfig juggling — unless you want to. Defaults are real defaults, not "minimal" placeholders.
2. **Industry-strength by default.** Every build is multi-arch. Every release tag is immutable. Every deploy is reproducible from a content fingerprint. Every action supports `--dry-run`. None of these are opt-in.
3. **Close the build-to-ship gap.** From `erun init` to a pod running your code in a real Kubernetes cluster is one workflow, not seven. The same workflow scales from your laptop to a managed cloud cluster.
4. **Best practices encoded, not lectured.** ERun's defaults *are* the best practices: stable release versions vs. snapshot iteration tags, content-derived build caches, explicit Helm hooks, idle-driven cost control. You learn the practices by using the tool.

## What you can do with ERun

- Spin up an isolated runtime pod for any project with one command (`erun init` + `erun open`).
- Iterate locally with snapshot builds that are safe to overwrite and instant to rebuild.
- Promote the same code to a non-local environment with stable, immutable release tags — no rebuild, no mutation, no surprises.
- Switch between local development and remote managed cloud clusters without changing your workflow.
- Talk to a remote pod's tooling over MCP for AI-assisted development.
- Stop paying for idle cloud compute automatically when you walk away.

## Where to start

- [Why ERun](/why) — the design principles in detail.
- [Install ERun](/getting-started/install).
- [Create your first environment](/getting-started/first-environment).
- [Concepts: tenants and environments](/concepts/tenants-and-environments).

## Project

ERun is open source under [github.com/sophium/erun](https://github.com/sophium/erun).
