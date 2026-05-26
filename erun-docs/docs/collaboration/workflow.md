---
title: Workflow
---

# Workflow

ERun isn't tied to a specific workflow tool or a fixed set of states. It aligns with the **first principles of operator-agent interaction**, and it slots under whatever states your organization already uses — Jira, GitHub Issues, Linear, Kanban, custom flows. Use ERun with your existing process; don't rewrite your process to use ERun.

## The first principles

Every workflow built on ERun shares the same shape:

1. **Each task has an Operator** — the human responsible for it.
2. **Work happens in an isolated environment** — driven by the Operator alone, by an Agent, or by both side by side.
3. **The Agent's autonomy depends on its maturity** for that class of work — Supervised or Autonomous-in-scope.
4. **Every action is auditable** — Operators preview before (`--dry-run`), replay after (action log).

State names, board columns, issue trackers — all incidental. The primitives above stay constant.

## Example states a team might use

<figure className="erun-hero-figure">
  <img src="/img/workflow-states.svg" alt="A task starts at TRIAGE. From there it can be accepted to TODO, then assigned to IN_PROGRESS, then review opened to PR, then build green to QA, then validated + merged to DONE. PR or QA can loop back to IN_PROGRESS as 'changes requested' or 'rework'. TRIAGE or PR can also branch to REJECTED as 'not pursued' or 'abandoned'." />
</figure>

| State | Meaning |
|---|---|
| **TRIAGE** | Idea or issue exists. Not committed to yet. |
| **TODO** | Accepted into the backlog, ready to pick up. |
| **IN PROGRESS** | Assigned. Work happening in an environment. |
| **PR** | Review opened — see [reviews](/collaboration/reviews). |
| **QA** | Build green, final validation. |
| **DONE** | Merged via the queue. |
| **REJECTED** | Abandoned at any stage. |

`PR` / `QA` / `DONE` map cleanly to the ERun review lifecycle (`OPEN` → `READY` → `MERGE` → `MERGED`). `TRIAGE` / `TODO` / `IN PROGRESS` sit above ERun as project-management states the Operator drives. Rename any of them; ERun's behavior doesn't change underneath.

## Who does what

| Stage | Operator | Agent |
|---|---|---|
| Triage | Accept / reject | Suggest |
| Backlog | Assign | Pick up |
| Work in progress | Join, take over, hand back | Work in env, post updates |
| Review (PR) | Comment, approve, request changes | Open, respond, push fixes |
| QA | Approve merge, reject | Run validation, post results |
| Done / Rejected | Final authority | — |

State labels change. The operator-agent split doesn't.

## Agent maturity stages

How much autonomy an agent has earned, **per class of work**:

- **Supervised** — the Agent runs the task end-to-end in its environment. The Operator reviews the resulting PR, not every command. Common steady state.
- **Autonomous (in scope)** — for pre-approved classes (dependency bumps, doc updates, generated-code regen, snapshot deploys, …), the Agent goes start-to-finish without per-task approval. The audit captures every action; the Operator can review any of it later.

Operators graduate an Agent from one stage to the next by observing past behavior in the audit log. Scope and graduation are recorded in tenant policy, not in any individual interaction — so trust is durable, not session-bound. The state machine the team uses is orthogonal to this: an Agent at **Supervised** maturity can act inside `IN_PROGRESS` of any workflow, whether the team calls that state "Doing", "Active", "In flight", or anything else.
