---
title: Glossary
---

# Glossary

The canonical vocabulary used across the ERun docs. Where two words could mean the same thing, only one is canonical here — pick that one when writing for or about ERun.

## Roles

**Operator** — the human responsible for the work. Drives ERun via the desktop app or terminal; supervises Agents via the [audit trail](/collaboration/operator-in-the-loop).

**Agent** — an AI assistant (Claude Code, Codex, custom MCP clients) attached to an environment via [MCP](/mcp/overview).

Both are first-class. Both share every environment. An env can have an Operator and an Agent, an Operator alone, or — for unattended automation — an Agent alone.

## Project scoping

**Tenant** — a project. One git repository.

**Environment (env)** — a workspace inside a tenant. Each env has its own worktree on disk, its own Kubernetes namespace, its own services. The canonical user-facing word for "a place where work happens."

**Namespace** — the Kubernetes primitive underneath an environment. Used only when referring to K8s internals; from the user side, say "environment."

## Environment types

**Agent env** — an environment tuned for development. Builds happen here (snapshot tags); Operator + Agent iterate here. Comes in two flavours:

- **local-agent** — worktree mounted from your local machine via `hostPath`. Best when the cluster runs on your laptop (Docker Desktop, OrbStack, …).
- **remote-agent** — worktree cloned to a PVC in the pod. Best when the cluster doesn't share your filesystem (managed cloud).

**Runtime env** — an environment for serving deployed services. No worktree, no builds; receives release-tagged artefacts via `erun deploy`. Common names: `dev`, `test`, `dr`, `prod`.

See [Environment types](/concepts/environment-types) for the full split.

## Interfaces

**Desktop app** — the ERun control panel. Macros, persistent terminals, IDE launchers, environment management.

**Terminal / CLI** — the `erun` command. Universal entry point; also the automation surface for CI.

**MCP** — Model Context Protocol. How Agents drive ERun: typed JSON-RPC tools (`idle`, `doctor`, `list`, `version`, `raw`) exposed on every env's runtime pod.

**SSH** — the IDE-attach endpoint on every env's runtime pod. VS Code Remote-SSH, IntelliJ Gateway, Cursor, Zed, JetBrains products, Neovim with remote plugins all attach here.

Three interfaces, one engine. See the anatomy diagram on the [introduction](/intro).

## Infrastructure

**Runtime pod** — one pod per env, hosting `erun-devops` (shell + tools), `erun-mcp` (MCP server), `erun-dind` (docker daemon). The shared surface for Operator (SSH) and Agent (MCP). See [Inside an environment](/concepts/runtime-pods).

**Cloud context** — a managed Kubernetes cluster ERun starts on demand and stops when idle. See [Cloud contexts](/concepts/cloud-contexts).

**Container registry** — durable store for built images. A project keeps a marked **list** of registries (each with `build`/`from`/`to`/`deploy` roles); the default is a single `ghcr.io/sophium` entry marked build + deploy. See [Container registries](/deployment/registries).

## Operations

**Snapshot tag** — `<semver>-snapshot-<UTC-timestamp>`. Disposable; used in agent envs.

**Release tag** — stable `<semver>` from the VERSION file. Immutable; used to promote artefacts to runtime envs.

**Fingerprint cache** — content-derived hash that promotes a build from cache when the Dockerfile and its `COPY` sources haven't changed.

**Deploy plan** — declared in `.erun/config.yaml` under `environments.<env>.k8s.deployments`. Ordered list of components (or parallel groups) to roll out.

## Workflow

**Audit trail** — every CLI invocation, every MCP call, every API write, recorded with the actor's identity. The mechanism by which Operators safely extend Agent autonomy over time.

**Review** — a unit of work-to-be-merged. Source branch, target branch, status, and references to its latest builds. See [Reviews](/collaboration/reviews).

**Merge queue** — shared per target branch. FIFO; `POST /v1/reviews/merge-queue/advance` promotes the head to `MERGED`.

## Non-canonical terms

These are heard in conversation but **avoid them in docs**:

| Don't say | Say instead |
|---|---|
| sandbox | environment (env) |
| user, human | Operator |
| AI assistant, bot, copilot | Agent |
| dev env | agent env |
| non-local env, prod env | runtime env (or just the env name) |
| ERun cluster | Kubernetes cluster |
