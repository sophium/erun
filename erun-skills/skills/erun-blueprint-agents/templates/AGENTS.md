# AGENTS.md

Repository guidance for humans and coding agents working in this erun tenant
project. Read this file first, then read any subdirectory `AGENTS.md` relevant to
the files you are about to touch.

`CLAUDE.md` is a symlink to this file, so both `AGENTS.md` (Codex) and `CLAUDE.md`
(Claude Code) resolve to one source of truth. Edit `AGENTS.md` only; never edit
`CLAUDE.md` directly.

## What this project is

This repo is a **tenant project** managed by
[erun](https://github.com/sophium/erun). Work happens against a **tenant** +
**environment** pair (`<tenant>` / `<env>`):

- An **agent env** (e.g. `<tenant>-local`) is the dev workbench where you build,
  author the deploy artifacts, and run coding agents. The desktop app deploys it.
- A **runtime env** (e.g. `<tenant>-prod`) is a serving environment an operator
  deploys published versions into.
- The runtime runs in a Kubernetes pod. You typically work **inside** it via
  `erun open`, which drops you in the pod with the repo worktree and the `erun`
  CLI, `git`, `gh`, and the baked skills already present. The env's
  `runtimeversion` pins the runtime image the pod runs.

## Core erun commands

Run these from a laptop or inside the pod (`erun <cmd> --help` documents each):

- `erun list` — list configured tenants and environments.
- `erun open` — open a shell in the tenant environment (the primary way agents
  work inside an env).
- `erun build` — build the project's container images and mint the version.
- `erun deploy` — install a published version into an environment.
- `erun terraform apply` — run the env's per-env Terraform from the right folder
  automatically (init → plan → confirm → apply).
- `erun doctor` — diagnose and repair an environment's runtime and config.

`build → push → deploy → open` are independent primitives: `build` mints the
version and the later steps thread it forward. Nothing decides *what else* to run
based on the environment — that policy lives in the caller.

## Where the deploy artifacts live

- **Terraform** (the cluster edge): `terraform-<tenant>/<env>/`. `common.tf` and
  `variables.tf` are canonical at the tree root and symlinked into each env
  folder; each env adds its services via its own `main.tf` + `<env>.tfvars`.
- **Helm** (platform component umbrellas, when used):
  `<tenant>-devops/k8s/<tenant>-<component>/`, each wrapping a published erun OCI
  chart with a per-env `values.<env>.yaml`.
- **Custom runtime image** (when used):
  `<tenant>-devops/docker/<tenant>-devops/Dockerfile`.

These are committed to git and shared with the team — not written to
`${ERUN_OUTPUTS_DIR}` (that directory is only for off-pod deliverables such as
reports or build outputs).

## One erun version pins everything

Every erun reference across the repo moves together, bumped as one after an
`erun upgrade` — never piecemeal:

1. Terraform module `source = "…?ref=v<version>"` (each tenant module wrapping an
   erun published module).
2. Helm umbrella `Chart.yaml` dependency `version:` (each wrapped erun OCI chart).
3. The build-env Dockerfile `FROM ghcr.io/sophium/erun-devops:<version>`.
4. The env's `runtimeversion`.

Resolve the target version with `erun version --no-registry` (in-pod) or the
env's `runtimeversion` (laptop).

## Skills

erun ships skills (Claude Code / Codex) for the common motions — invoke one by
describing the intent:

- **`erun-blueprint-platform`** — lay down / maintain the Terraform + Helm deploy
  wiring for a hosted erun platform.
- **`erun-build-env`** — extend the runtime image with your own toolchain.
- **`erun-blueprint-api`** / **`erun-blueprint-rls-db`** / **`erun-blueprint-docs`**
  — build a multi-tenant API, a row-level-security Postgres database, or a docs
  site.
- **`erun-contribute`** / **`erun-file-issue`** — land a change in, or file an
  issue against, erun itself.

## Agent-guidance files

`AGENTS.md` is the canonical file; `CLAUDE.md` is a same-directory relative
symlink to it (git mode `120000`). On a Windows checkout without symlink support
(`core.symlinks=true` / Developer Mode off), git materializes `CLAUDE.md` as a
plain text file containing the word `AGENTS.md` rather than a real link — read
`AGENTS.md` directly there.
