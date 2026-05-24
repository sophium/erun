---
title: Why ERun
slug: /why
---

# Why ERun

ERun exists because the path from "a developer with an idea" to "that idea running on production-grade infrastructure" is too long, too lossy, and almost entirely accidental complexity.

The accidental complexity looks like this:

- Pick a container registry. Configure authentication. Configure it differently per environment.
- Make sure your image builds for both your laptop's CPU architecture and your cluster's.
- Write Helm charts. Or Kustomize manifests. Or both. Keep them in sync.
- Decide your release tagging scheme. Hope nobody overwrites a stable tag with a debug build.
- Set up CI to push tags. Set up CD to deploy them. Pray they're consistent.
- Spin up a Kubernetes cluster for development. Pay for it 24/7 even when nobody is using it.
- When something breaks, SSH into a pod, hope the right tools are installed, and start grepping.

None of that is what developers want to spend their day on. ERun's premise is that all of it is solvable once and shipped to everyone — with sane defaults that *are* industry best practices, not weakened approximations of them.

## The four principles

### 1. Developer-first

The primary interface is a single CLI binary and an optional desktop app. The minimum viable interaction is two commands:

```bash
erun init <tenant> <env>
erun open <tenant> <env>
```

That's it. You get an isolated runtime pod with your repo checked out, Docker-in-Docker, Helm, kubectl, an MCP server for AI tooling, and a shell ready to go.

There are no required YAML files to author. No kubeconfig to merge. No `helm install` invocations to memorize. The tool resolves defaults from your project, your tenant config, and the current cluster state, and shows you what it's about to do.

### 2. Industry-strength by default

The "good enough for prod" defaults are the *only* defaults:

- **Multi-architecture builds are unconditional.** Every `erun build`, `erun deploy`, and `erun build --release` produces both `linux/amd64` and `linux/arm64`. There is no single-arch code path — so an arch-specific Dockerfile bug fails at developer-machine build time, not at remote deploy time.
- **Release tags are immutable.** Non-local environments use bare semver tags. `erun push` from a non-local env refuses to rebuild and overwrite — promotion is an explicit, reviewable step.
- **Builds are content-fingerprinted.** Every Docker build computes a content hash over the Dockerfile and its `COPY` sources. The next build pulls the published image tagged with that fingerprint and promotes it instead of rebuilding. Fresh clones get pinned bases without a 10-minute compile.
- **Every action supports `--dry-run`.** Dry-run prints the real commands ERun would run, with secrets redacted. The trace lines are the same lines a real run emits. There's no "but does it actually do that?" — `--dry-run` is the contract.

### 3. Close the build-to-ship gap

The same workflow scales from your laptop to a managed cloud cluster. Specifically:

| | Local | Non-local |
|---|---|---|
| Where it runs | Your Docker Desktop / kind / k3d | A real Kubernetes cluster on AWS, GCP, on-prem |
| Build behavior | Snapshot tags, auto-rebuild on `push` | Stable release tags, explicit `build` + `push` |
| Deploy command | `erun deploy` | `erun deploy` |
| Tooling inside the pod | identical | identical |
| MCP / SSH / IDE attach | identical | identical |

Moving from "works on my machine" to "works on prod" is changing one environment name, not learning a new tool.

### 4. Best practices encoded, not lectured

ERun doesn't have a chapter in its docs titled "you should use immutable release tags". It just *uses* immutable release tags. The way the tool works *is* the practice.

A non-exhaustive list of practices ERun bakes in:

- **Reproducible builds.** Fingerprint cache + pinned base images via `docker.fingerprints` in `.erun/config.yaml`.
- **Per-environment isolation.** Each environment is its own Kubernetes namespace, its own PVCs, its own RBAC scope.
- **Cost control by default.** Managed cloud environments idle-stop the underlying compute after configurable inactivity. The next `erun open` brings them back.
- **Diagnostics over SSH.** The in-pod MCP server exposes structured tools (`doctor`, `idle`, `list`, `raw`) over JSON-RPC. Most "what's happening inside the pod?" questions are answered without an interactive shell.
- **Engineering rules encoded.** `AGENTS.md` files in each module capture the rules that apply when changing that subtree — read by humans and by AI agents alike, so the rules stay enforced over time.

## What ERun is not

To be specific about scope:

- ERun is not a CI/CD replacement. It pairs with GitHub Actions, GitLab CI, etc. — but it gives you a much smaller surface to wire into them.
- ERun is not a serverless platform or a function runtime. It runs container images on Kubernetes.
- ERun does not abstract away Kubernetes. You still have a real cluster; you can still `kubectl exec` if you want to. ERun just removes the daily friction of using one.

## Where this leads

The endpoint is straightforward: a developer can clone a project, run `erun init` and `erun open`, and within minutes be iterating on code that runs the same way locally as it does in production — on industry-strength infrastructure, with industry best practices applied by default, without having authored a single YAML manifest.

That's the gap ERun closes.
