---
title: Why ERun
slug: /why
---

# Why ERun

ERun exists because the path from "a developer with an idea" — or an AI agent acting on a developer's behalf — to "that idea running on production-grade infrastructure" is too long, too lossy, and almost entirely accidental complexity.

The accidental complexity looks like this:

- Pick a container registry. Configure authentication. Configure it differently per environment.
- Make sure your image builds for both your laptop's CPU architecture and your cluster's.
- Write Helm charts. Or Kustomize manifests. Or both. Keep them in sync.
- Decide your release tagging scheme. Hope nobody overwrites a stable tag with a debug build.
- Set up CI to push tags. Set up CD to deploy them. Pray they're consistent.
- Spin up a Kubernetes cluster for development. Pay for it 24/7 even when nobody is using it.
- When something breaks, SSH into a container, hope the right tools are installed, start grepping — and hope an AI agent can do the same.

None of that is what developers want to spend their day on, and none of it is something an agent can do reliably without exhaustive prompting. ERun's premise is that all of it is solvable once and shipped to everyone — with sane defaults that *are* industry best practices, and with a surface that's equally legible to humans and to agents.

## The primary aim: agentic coding

ERun is designed around the assumption that **AI agents are first-class users of the platform** — not just developers using AI as an editor, but agents that initialize environments, build images, deploy charts, diagnose running environments, and iterate. And not one at a time: a single laptop can host many isolated environments in parallel, so an organization can run multiple agents — one per task, one per feature branch, one per developer — without them stepping on each other.

Concretely, that means:

- **Structured MCP server in every environment.** Each environment runs an `erun-mcp` container that exposes typed tools — `idle`, `doctor`, `list`, `version`, `raw` — over JSON-RPC 2.0. Most questions an agent needs to ask ("what's the current state of this environment?", "is the runtime healthy?", "what would `erun deploy` do right now?") are answered without an interactive shell and without parsing free-form text.
- **`--dry-run` as a binding contract.** Every action-oriented command can produce its plan as trace lines. The trace lines are the same lines a real run emits — `cd /home/erun/git/my-repo && docker build ...`, `helm upgrade --install ...` — so an agent that previews a command before running it gets a faithful preview, not a summary.
- **`AGENTS.md` everywhere.** Each module declares its engineering rules in an `AGENTS.md` file. These rules apply to humans and to agents alike: which patterns to use, which to avoid, which preflight checks to run before commands that touch shared systems. Agents are expected to read them; the rules are versioned with the code, so the constraints stay enforced as the codebase evolves.
- **Deterministic commands.** Commands are designed to be safe to run repeatedly and in parallel — no hidden global state, no required interactive prompts on MCP-exposed paths, no surprises on retry.
- **Per-environment MCP port-forwards.** The desktop app keeps a port-forward open to each open environment's MCP container. The forward port is published in a small JSON file at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json` so an agent on the same machine can discover and call the right endpoint without orchestration. With many environments open at once, each gets its own port — agents talking to environment A never accidentally reach into environment B.
- **Cross-agent collaboration via the erun API.** Per-environment MCP handles in-environment questions. The hosted erun API handles cross-environment, cross-agent state: reviews (with a full `OPEN → READY → MERGE → MERGED` lifecycle), threaded comments anchored to a commit and a line, recorded build results, and a shared merge queue per target branch. Agents can post comments on each other's work, react to each other's build outcomes, and advance the queue — all over the same OIDC-authenticated REST surface a human would use, with every action audited and scoped to the right tenant. See [Agent collaboration](/collaboration/overview) for the model and the endpoints.

The net effect: an agent can pick up an idea, scaffold an environment, iterate on code, deploy, review a peer's work, and audit the result — without escaping into ad-hoc shell commands or proprietary glue, and without operating in isolation from the other agents on the team.

## Iteration speed

Speed isn't just about latency — it's about how many friction points there are between "I want to try a change" and "the change is running in a real environment."

- **Snapshot vs release tags.** In the `local` environment, `erun build` produces unique snapshot tags (`X.Y.Z-snapshot-<UTC-timestamp>`) that are safe to overwrite on every iteration. In non-local environments, the tag is the bare semver from `VERSION` — stable and immutable. The split lets you iterate as fast as your build pipeline allows without ever risking a release artifact.
- **Fingerprint cache promotion.** Every Docker build computes a content fingerprint over the Dockerfile and its `COPY` sources. The next build pulls the published image tagged with that fingerprint and *promotes* it locally instead of rebuilding. A fresh clone of the repo gets a pinned base image without a 10-minute compile.
- **One-command workflows.** `erun init` → `erun open` is the entire on-ramp. `erun deploy` is the entire shipping path. No `kubectl create namespace`, no `helm upgrade --install --create-namespace --values ...`, no `aws ecr get-login-password ...`. Defaults are real defaults.
- **Idle-stop on cloud environments.** Managed cloud contexts shut down the underlying compute after a configurable inactivity timeout. The next `erun open` brings them back. You don't pay for what you're not using; you don't have to remember to stop anything.
- **Same workflow, laptop to cluster.** Switching between `local` and a managed cloud environment is changing one environment name. The CLI, the MCP surface, the tooling inside the environment — all identical.

## Compliance preserved by default

The hard part of building "fast" platforms is that "fast" usually means "skips checks." ERun's defaults are written so the fast path is also the compliant path.

- **Immutable release tags.** `erun push` from a non-local environment refuses to rebuild and overwrite. Promotion to a stable tag is an explicit, reviewable step. A release artifact is what it says it is, not what your laptop happened to have in its Docker cache.
- **Multi-architecture as a release gate.** Every release-tagged image is multi-arch (`linux/amd64` + `linux/arm64`). The build flow refuses to publish a single-arch artifact. Arch-specific Dockerfile bugs fail at developer-machine build time, not at remote deploy time.
- **Per-environment isolation.** Each environment lives in its own Kubernetes namespace with its own PVCs, its own ServiceAccount, its own RBAC scope. Tenants cannot see each other's data, and environments within a tenant cannot see each other's docker daemon, home volume, or secrets.
- **Cloud contexts bind to specific accounts.** A managed cloud environment is bound to a specific cloud provider alias, account, region, and instance. The chart records these as labels on the deployment, so an audit of a running environment can trace back to the exact cloud identity that owns it.
- **Auditable dry-run traces.** Every command can produce its full action plan ahead of time. The plan is the source of truth for change control — review the trace, then run for real.
- **Engineering rules versioned with the code.** `AGENTS.md` files are part of the repo, reviewed in PRs, enforced by the team. Rule drift is visible in `git log`, not in a Confluence page nobody reads.

## What ERun is not

To be specific about scope:

- ERun is not a CI/CD replacement. It pairs with GitHub Actions, GitLab CI, etc. — but it gives you a much smaller surface to wire into them.
- ERun is not a serverless platform or a function runtime. It runs container images on Kubernetes.
- ERun does not abstract away Kubernetes. You still have a real cluster; you can still `kubectl exec` if you want to. ERun just removes the daily friction of using one, and gives agents a structured surface that doesn't require shell parsing.

## Where this leads

The endpoint is straightforward. A person, an organization, or an AI agent acting on their behalf can clone a project, run `erun init` and `erun open`, and within minutes be iterating on code that runs the same way locally as it does in production — on industry-strength infrastructure, with industry best practices applied by default, with full audit traceability, and at the pace agentic coding actually demands.

That's the gap ERun closes.
