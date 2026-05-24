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
- **`--dry-run` for transparency and control.** Every action-oriented command can produce its full action plan before executing — the exact `docker build`, `helm upgrade`, `git push` commands ERun would run, with secrets redacted. The operator (or the agent, when delegating to one) reviews the plan, then runs it. The trace lines are identical to a real run, so what you see in dry-run is exactly what would happen — not a summary, not a paraphrase.
- **`AGENTS.md` everywhere.** Each module declares its engineering rules in an `AGENTS.md` file. These rules apply to humans and to agents alike: which patterns to use, which to avoid, which preflight checks to run before commands that touch shared systems. Agents are expected to read them; the rules are versioned with the code, so the constraints stay enforced as the codebase evolves.
- **Deterministic commands.** Commands are designed to be safe to run repeatedly and in parallel — no hidden global state, no required interactive prompts on MCP-exposed paths, no surprises on retry.
- **Per-environment MCP port-forwards.** The desktop app keeps a port-forward open to each open environment's MCP container. The forward port is published in a small JSON file at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json` so an agent on the same machine can discover and call the right endpoint without orchestration. With many environments open at once, each gets its own port — agents talking to environment A never accidentally reach into environment B.
- **Cross-agent collaboration via the erun API.** Per-environment MCP handles in-environment questions. The hosted erun API handles cross-environment, cross-agent state: reviews (with a full `OPEN → READY → MERGE → MERGED` lifecycle), threaded comments anchored to a commit and a line, recorded build results, and a shared merge queue per target branch. Agents can post comments on each other's work, react to each other's build outcomes, and advance the queue — all over the same OIDC-authenticated REST surface a human would use, with every action audited and scoped to the right tenant. See [Agent collaboration](/collaboration/overview) for the model and the endpoints.

The net effect: an agent can pick up an idea, scaffold an environment, iterate on code, deploy, review a peer's work, and audit the result — without escaping into ad-hoc shell commands or proprietary glue, and without operating in isolation from the other agents on the team.

## Operator in the loop, by default

The operator — the human responsible for the work — is a first-class citizen of the platform. Agents are powerful, but the operator retains control, sees what they're doing, and can take over at any time. This is not an obstacle to agentic coding; it is the prerequisite for it.

- **The desktop is the operator's control panel.** Start, stop, and inspect every environment from the sidebar. Open any of them in a shell, in VS Code (`--vscode`), in IntelliJ (`--intellij`), or in any other Remote-SSH-capable IDE — the chart exposes an SSH endpoint that any modern editor can attach to. The operator can `git diff`, run a test, make a commit, or simply watch — and this works equally well as a primary development surface with no agent involved at all.
- **Humans and agents share one environment.** Claude, Codex, and other AI agents connect to the same environment over MCP that the operator is working in. There is one workspace, one docker daemon image cache, one audit trail. A commit the operator makes in their IDE is immediately visible to the agent's `git diff`; an action the agent takes shows up in the operator's terminal session.
- **Every action the agent took is replayable.** The CLI's `audit:` trace line records each command. `--dry-run` returns the same trace as a real run. Every write to the erun API (review, comment, build, status transition) is persisted with the actor's identity. There is no anonymous agent action and no unrecoverable change.
- **Take-over is cheap.** If an agent goes off-course, the operator joins in one command. There is no place an agent can run where the operator cannot follow — and once joined, the operator can suspend, correct, or continue the work.
- **Delegation scope is explicit.** OIDC + tenant scoping define what an agent may touch. Per-environment isolation defines where it may touch it. The operator can pre-approve a class of changes (dependency bumps, code regeneration, snapshot deploys) without pre-approving every individual action.

Crucially, this is the path to **eventual agent autonomy**. Operators don't earn agent trust by removing themselves from the loop; they earn it by extending the scope of pre-approved delegation as the audit trail accumulates evidence that the agent behaves as expected. ERun's job is to make sure the audit and control infrastructure scales ahead of the autonomy, not behind it.

See [Operator in the loop](/collaboration/operator-in-the-loop) for the full model.

## Iteration speed

Speed isn't just about latency — it's about how many friction points there are between "I want to try a change" and "the change is running in a real environment."

- **Snapshot vs release tags.** In the `local` environment, `erun build` produces unique snapshot tags (`X.Y.Z-snapshot-<UTC-timestamp>`) that are safe to overwrite on every iteration. In non-local environments, the tag is the bare semver from `VERSION` — stable and immutable. The split lets you iterate as fast as your build pipeline allows without ever risking a release artifact.
- **Fingerprint cache promotion.** Every Docker build computes a content fingerprint over the Dockerfile and its `COPY` sources. The next build pulls the published image tagged with that fingerprint and *promotes* it locally instead of rebuilding. A fresh clone of the repo gets a pinned base image without a 10-minute compile.
- **One-command workflows.** `erun init` → `erun open` is the entire on-ramp. `erun deploy` is the entire shipping path. No `kubectl create namespace`, no `helm upgrade --install --create-namespace --values ...`, no `aws ecr get-login-password ...`. Defaults are real defaults.
- **Idle-stop on cloud environments.** Managed cloud contexts shut down the underlying compute after a configurable inactivity timeout. The next `erun open` brings them back. You don't pay for what you're not using; you don't have to remember to stop anything.
- **Same workflow, laptop to cluster.** Switching between `local` and a managed cloud environment is changing one environment name. The CLI, the MCP surface, the tooling inside the environment — all identical.

## Compliance preserved by default

Compliance in ERun isn't a checklist of separate practices bolted onto a development workflow. It **is** the development workflow. Every environment is a controlled, isolated, audited substrate, and the same controls apply whether an agent or an operator is working in it. That control surface is what makes results repeatable — and what makes the platform fast at the same time, because the controlled path is also the default path.

The controls:

- **Isolated environment per task.** Each environment is its own Kubernetes namespace with its own PVCs, ServiceAccount, RBAC scope. An agent or operator working in one cannot reach into another's state.
- **Same controls for agents and operators.** OIDC authenticates both. Tenant scoping applies to both. Audit logs record both. There is no "agent escape hatch" that bypasses what a human has to go through, and no "operator override" that bypasses the audit.
- **Auditable trace for every action.** Every CLI command emits an `audit:` line plus per-action trace lines. `--dry-run` reveals the same plan ahead of time — operators preview, then approve. Every write to the erun API (review, comment, build, status transition) is persisted with the actor's identity. The trace is the source of truth for change control.
- **Cloud contexts bound to specific identities.** A managed cloud environment is bound to a specific cloud provider alias, account, region, and instance. The chart records these as labels on the deployment, so an audit of a running environment can trace back to the exact cloud identity that owns it.
- **Immutable release tags.** Non-local environments use bare semver tags from the `VERSION` file; `erun push` from a non-local env refuses to rebuild and overwrite. Promotion to a stable tag is an explicit, reviewable step — a release artifact is what it says it is, not what someone's laptop happened to have in its Docker cache.
- **Engineering rules versioned with the code.** `AGENTS.md` files in each module are part of the repo, reviewed in PRs, enforced by the team. Rule drift is visible in `git log`, not in a Confluence page nobody reads.

Reproducibility is the natural consequence: when the environment is controlled, the same inputs produce the same outputs. Multi-architecture build verification, fingerprint cache promotion, and pinned base images live inside this envelope as quality-and-speed tactics — they make the controlled environment work efficiently in practice — not as separate compliance items.

### Why this means more speed, not less

Compliance frameworks usually slow teams down because they live alongside the development workflow as a parallel set of obligations. ERun's framing inverts that:

- **Repeatable results** mean you don't lose time chasing "works on my machine" bugs.
- **A shared compliance baseline** means an operator doesn't have to bespoke-audit every agent action — the audit already exists.
- **A controlled environment scales.** Opening environment number fifty costs the same as opening environment number one, because the controls are the platform, not artisanal per-environment work.

The compliance surface is also the speed surface. That's the point.

## What ERun is not

To be specific about scope:

- ERun is not a CI/CD replacement. It pairs with GitHub Actions, GitLab CI, etc. — but it gives you a much smaller surface to wire into them.
- ERun is not a serverless platform or a function runtime. It runs container images on Kubernetes.
- ERun does not abstract away Kubernetes. You still have a real cluster; you can still `kubectl exec` if you want to. ERun just removes the daily friction of using one, and gives agents a structured surface that doesn't require shell parsing.

## Where this leads

The endpoint is straightforward. A person, an organization, or an AI agent acting on their behalf can clone a project, run `erun init` and `erun open`, and within minutes be iterating on code that runs the same way locally as it does in production — on industry-strength infrastructure, with industry best practices applied by default, with full audit traceability, and at the pace agentic coding actually demands.

That's the gap ERun closes.
