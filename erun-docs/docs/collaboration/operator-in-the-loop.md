---
title: Operator in the loop
---

# Operator in the loop

In ERun, the **operator** — the human responsible for the work — is a first-class citizen of the platform. Agents are powerful tools, but the operator retains control: they can open any environment in a shell or an IDE, develop directly, watch what an agent is doing, review every action it took, take over, hand back, or steer. Every agent action is captured in an audit trail that the operator can replay.

The operator is not just a supervisor. They can also be the **primary developer** in an environment — opening it in VS Code or IntelliJ and writing code themselves, with no agent in the loop at all. ERun is a great agentic platform; it is equally a great clean dev environment for humans alone. Both modes are first-class, and the operator decides which one applies on any given task.

When agents are involved, the operator-control and audit infrastructure is what makes it safe to extend their autonomy over time. You can't trust an agent with more responsibility than you can observe.

## What "first-class operator" means in practice

| Capability | How ERun delivers it |
|---|---|
| Join an agent's environment | `erun open <tenant> <env>` attaches a real shell to the same runtime pod the agent is using. The operator and the agent share `/home/erun`, the docker daemon, and the workspace. |
| See live activity | The desktop app's terminal sessions show what's happening in real time. The MCP server's `idle` and `list` tools return current state. |
| Audit every agent action | Every CLI command runs through `auditCommand` and emits a trace line. `--dry-run` shows the operator the same plan ahead of time — preview, then approve. Every API write (review status change, comment, build) is persisted with the actor's identity. |
| Review and revert | All changes flow through git. Comments and reviews in the erun API are append-only with status transitions. Build outcomes are recorded, not transient. |
| Take over an in-flight task | The operator opens the agent's environment, suspends the agent (or lets it finish its current step), continues in the shell, then hands back. |
| Delegate selectively | The operator can scope what the agent is allowed to do via tenant-level permissions and OIDC issuer trust — the same controls a human collaborator would have. |

## The audit trail

Three layers of audit, all readable by the operator:

1. **In-environment trace** — every `erun` invocation emits a `audit: erun <cmd> <args>` line and per-action trace lines (e.g. the exact `docker build`, `helm upgrade`, `git push` commands it ran). The same lines appear in `--dry-run` mode so the operator can preview a command before running it; the dry-run trace and the real-run trace match.
2. **Per-environment MCP** — `idle.activity` records terminal activity and network traffic windows. `doctor` records inspection outcomes. `raw` records the exact `argv` run and the working directory.
3. **Hosted erun API** — every review, comment, status transition, and recorded build is persisted with `creator_user_id` and timestamps. An audit-events table captures security-relevant events. Nothing an agent does via the API is anonymous; nothing is unrecoverable.

For a security-sensitive action — a release tag push, a merge-queue advance, a status transition — the operator can reconstruct *who* did *what*, *when*, in *which environment*, and *with which build*.

## Working in the environment

`erun open` is the single entry point for every operator workflow. Three attach modes cover the common cases — all use the same underlying environment, the same audit trail, the same MCP endpoint.

### Shell

```bash
erun open my-tenant feature-a
```

Attaches a terminal to the runtime pod. Use it for `git diff`, `git log`, running tests, ad-hoc commands, watching an agent's session in real time.

### VS Code

```bash
erun open my-tenant feature-a --vscode
```

Launches VS Code's Remote-SSH against the in-pod SSH server. The full editor — extensions, language servers, debugger, integrated terminal — runs against the environment's filesystem. From the operator's perspective it is a local IDE; everything beneath is the same isolated environment an agent would use.

### IntelliJ IDEA

```bash
erun open my-tenant feature-a --intellij
```

Launches IntelliJ IDEA Gateway against the same SSH server. Same model as VS Code.

### What's shared regardless of attach mode

All three modes — shell, VS Code, IntelliJ — operate on the same runtime pod:

- Same `/home/erun/git/<repo>` workspace.
- Same docker daemon image cache.
- Same MCP endpoint (so an in-IDE agent and a shell-side agent see the same `idle`/`doctor`/`list` results).
- Same audit trail.

This means: an agent working in `feature-a` and an operator who opens that same environment in VS Code are not in parallel universes. They are looking at the exact same workspace, in real time. A commit one makes is immediately visible to the other.

### With or without an agent

These attach modes are not specific to supervising agents. The operator can use them for entirely human-driven work:

- Spin up `erun open my-tenant scratch --vscode` to explore a problem.
- Use `erun open my-tenant feature-b` as a clean, isolated dev environment for code you're writing yourself.
- Run many environments in parallel — one for each task — without agents anywhere in the picture.

The platform doesn't care whether an agent is present. The same isolation, audit, and tooling apply.

## Delegating to the agent

The operator decides what work to hand off. Common patterns:

- **Scoped task**: open a review with a clear name and target branch (`POST /v1/reviews`). The agent picks it up, opens the matching environment, iterates, records builds, and signals `READY`.
- **Constrained autonomy**: the operator pre-approves a class of changes (e.g. dependency bumps, generated-code regeneration) via tenant policy. The agent can act without per-action approval inside that scope.
- **Pair-style review**: the agent does the work, opens a review with comments explaining decisions. The operator reviews, leaves counter-comments, the agent addresses them. Same shape as two engineers collaborating.

## The path to eventual autonomy

The structure ERun puts in place — operator-in-the-loop today, with full audit and the ability to join any agent's environment — is what makes eventual autonomy safe. Trust is built on three observations:

1. **Past and future behavior are both visible.** `--dry-run` answers "what would this agent do?"; the persistent action log answers "what did this agent do?" — at the same fidelity. The operator never has to take an agent's word for either.
2. **Scope is enforceable.** OIDC, tenant scoping, and the per-environment isolation model put real walls around what an agent can touch.
3. **Take-over is cheap.** If an agent goes off-course, the operator joins the environment in one command. There's no "agent runaway" because there's no place an agent can run where an operator can't follow.

As these guarantees hold for more classes of work, operators can extend the agent's autonomy — graduating from per-action approval to per-task delegation to per-class autonomy — without ever blinding themselves to what's happening. ERun's job is to make sure the audit and control infrastructure scales ahead of the autonomy, not behind it.

## Where to look in the platform

- The CLI's `audit:` trace and per-command `--dry-run` (every command).
- The MCP `idle.activity` and `doctor` tools (every environment).
- The erun API's review history, comment threads, and build records (every tenant).
- The desktop app's per-environment terminal sessions (live view + replayable scrollback).

Together, these form the operator's control surface. They are not optional — they are how ERun works by default.
