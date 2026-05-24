---
title: Operator in the loop
---

# Operator in the loop

In ERun, the **operator** — the human responsible for the work — is a first-class citizen of the platform. Agents are powerful tools, but the operator retains control: they can join any agent's environment, watch what it's doing, review every action it took, take over, hand back, or steer. Every agent action is captured in an audit trail that the operator can replay.

This isn't a brake on agentic coding. It's the foundation for it: the operator-control and audit infrastructure is what makes it safe to extend an agent's autonomy over time. You can't trust an agent with more responsibility than you can observe.

## What "first-class operator" means in practice

| Capability | How ERun delivers it |
|---|---|
| Join an agent's environment | `erun open <tenant> <env>` attaches a real shell to the same runtime pod the agent is using. The operator and the agent share `/home/erun`, the docker daemon, and the workspace. |
| See live activity | The desktop app's terminal sessions show what's happening in real time. The MCP server's `idle` and `list` tools return current state. |
| Audit every agent action | Every CLI command runs through `auditCommand` and emits a trace line. `--dry-run` returns the same trace lines as a real run. Every API write (review status change, comment, build) is persisted with the actor's identity. |
| Review and revert | All changes flow through git. Comments and reviews in the erun API are append-only with status transitions. Build outcomes are recorded, not transient. |
| Take over an in-flight task | The operator opens the agent's environment, suspends the agent (or lets it finish its current step), continues in the shell, then hands back. |
| Delegate selectively | The operator can scope what the agent is allowed to do via tenant-level permissions and OIDC issuer trust — the same controls a human collaborator would have. |

## The audit trail

Three layers of audit, all readable by the operator:

1. **In-environment trace** — every `erun` invocation emits a `audit: erun <cmd> <args>` line and per-action trace lines (e.g. the exact `docker build`, `helm upgrade`, `git push` commands it ran). The trace is the same whether the command ran for real or with `--dry-run`.
2. **Per-environment MCP** — `idle.activity` records terminal activity and network traffic windows. `doctor` records inspection outcomes. `raw` records the exact `argv` run and the working directory.
3. **Hosted erun API** — every review, comment, status transition, and recorded build is persisted with `creator_user_id` and timestamps. An audit-events table captures security-relevant events. Nothing an agent does via the API is anonymous; nothing is unrecoverable.

For a security-sensitive action — a release tag push, a merge-queue advance, a status transition — the operator can reconstruct *who* did *what*, *when*, in *which environment*, and *with which build*.

## Joining the agent's environment

When an agent is working in `my-tenant/feature-a`, the operator can join in seconds:

```bash
erun open my-tenant feature-a
```

That attaches the operator's shell to the same runtime pod. Both the agent and the operator see `/home/erun/git/<repo>` at the same state, share the docker daemon's image cache, and see the same MCP endpoint. The operator can:

- `git log` to see what the agent has committed.
- `git diff` to see uncommitted work.
- Run `erun list` to see the resolved environment state.
- Use `kubectl exec` or the desktop terminal sessions UI to watch in real time.
- Make a commit themselves, run a test, fix a bug — then leave the environment for the agent to pick up again.

There's no special "operator mode." It's the same `erun open` an agent uses. The operator just brings human judgement.

## Delegating to the agent

The operator decides what work to hand off. Common patterns:

- **Scoped task**: open a review with a clear name and target branch (`POST /v1/reviews`). The agent picks it up, opens the matching environment, iterates, records builds, and signals `READY`.
- **Constrained autonomy**: the operator pre-approves a class of changes (e.g. dependency bumps, generated-code regeneration) via tenant policy. The agent can act without per-action approval inside that scope.
- **Pair-style review**: the agent does the work, opens a review with comments explaining decisions. The operator reviews, leaves counter-comments, the agent addresses them. Same shape as two engineers collaborating.

## The path to eventual autonomy

The structure ERun puts in place — operator-in-the-loop today, with full audit and the ability to join any agent's environment — is what makes eventual autonomy safe. Trust is built on three observations:

1. **Past behavior is replayable.** The dry-run contract plus persistent action logs mean an operator can ask "what would this agent do?" or "what did this agent do?" with the same fidelity.
2. **Scope is enforceable.** OIDC, tenant scoping, and the per-environment isolation model put real walls around what an agent can touch.
3. **Take-over is cheap.** If an agent goes off-course, the operator joins the environment in one command. There's no "agent runaway" because there's no place an agent can run where an operator can't follow.

As these guarantees hold for more classes of work, operators can extend the agent's autonomy — graduating from per-action approval to per-task delegation to per-class autonomy — without ever blinding themselves to what's happening. ERun's job is to make sure the audit and control infrastructure scales ahead of the autonomy, not behind it.

## Where to look in the platform

- The CLI's `audit:` trace and per-command `--dry-run` (every command).
- The MCP `idle.activity` and `doctor` tools (every environment).
- The erun API's review history, comment threads, and build records (every tenant).
- The desktop app's per-environment terminal sessions (live view + replayable scrollback).

Together, these form the operator's control surface. They are not optional — they are how ERun works by default.
