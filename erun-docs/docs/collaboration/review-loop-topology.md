---
title: Review loop topology
---

# Review loop topology

The [review → comment → fix loop](/collaboration/agent-patterns#5-review--comment--fix-loop) and the [Build Agent / Review Agent](/collaboration/workflow#specialised-agents-collaborating-via-tasks) row in the Workflow page both describe the shape abstractly. This page is the concrete version: two standing agent roles, each in its own environment, and exactly which review state each owns.

## The topology

One review moves between two environments as it goes from a proposed change to a merged one:

| Review state | Who owns it | What happens |
|---|---|---|
| — | **builder** | Implements the feature in its own environment. |
| `OPEN` → `READY` | **builder** | Takes the change from "done" to a review sitting at `READY` — see [Reviews](/collaboration/reviews). |
| `READY` | **reviewer** | Picks up reviews it is a reviewer on, reads the diff, posts line-anchored comment threads, and — where it has a concrete fix — pushes a proposal branch. |
| `READY`, threads open | **builder** | Reads the threads, judges each proposal on merit, merges the ones it accepts, pushes, rebuilds. It is not obliged to take a proposal, and says why when it declines, in that thread. |
| `READY`, threads open | **reviewer** | Returns to its own threads, reads the builder's replies, and resolves the ones the builder addressed. |
| `READY`, all resolved | **builder** | Advances the merge queue (`erun review queue advance`). |
| `MERGE` → `MERGED` | platform | The merge queue's gate builds the prospective merge and pushes only on green — see [Merge queue § The gate](/collaboration/merge-queue#the-gate). |

## Why two environments, not two agents in one

**Environment isolation is what makes the loop work, not a preference.** Two constraints force it, both enforced by erun today rather than merely recommended:

- **A reviewer needs its own checkout to push a proposal branch, and pushing from inside the builder's environment collides with the builder's own tree.** `erun exec push` resolves the branch to push from the working tree's *actual current branch* and refuses outright when the two disagree (`PushWorkingTreeBranch` in `erun-common/exec_push.go`) — it does not take the branch name as an instruction to check out, only as a declaration to verify. A reviewer proposing a fix branches from the review's head and pushes that new branch; doing so inside the builder's own worktree means checking out the proposal branch on top of whatever the builder currently has checked out — the branch under review — which is exactly the environment the builder may still be editing.
- **A runtime environment has no worktree to review from.** [Environment types](/concepts/environment-types) is explicit: a `runtime` env has no worktree at all — "no development happens here." `erun exec push`, `erun exec commit`, and `erun exec raw` all resolve a project root from an actual git checkout; a sourceless runtime pod has none (its `~/git/<repo>` is, at most, a read-only symlink to baked release artifacts, never a writable clone with a remote). So a runtime env cannot fetch a diff, post a comment against a real checkout, or push a proposal branch — the reviewer role needs a `local-agent` or `remote-agent` environment, the only two types with worktrees.

**The reviewer builds nothing**, so it never contends with the builder's Docker daemon or its BuildKit cache either — reviewing is read the diff, comment, optionally push a small proposal branch, never `erun build`.

Both are ordinary agent environments. Neither is a dedicated build environment: every erun environment is already a certified build environment, and the builder's own release build runs where the change already lives.

## The reviewer must come back

**Only a thread's root comment author can close it.** `PATCH /v1/reviews/{id}/comments/{commentId}/status` acts on the thread as a whole through its root, and only the root's own author may call it — see [Comments](/collaboration/comments#comment-status). This means the builder cannot resolve a reviewer's thread no matter how completely it addressed the point; the reviewer agent watching for "the author replied to my thread" and resolving it is not a nicety, it is the only thing that unblocks the merge short of an audited override.

`erun review queue advance` refuses to promote a review with any thread still `OPEN` (its root comment unresolved), naming the unresolved count in its `409` response. The one deliberate escape is `erun review queue override-advance`: a distinct, separately-authorized call that records its `reason` in the audit trail alongside the caller's identity — see [Merge queue § Overriding the gate](/collaboration/merge-queue#overriding-the-gate). It exists for exactly the case a reviewer never comes back; it is not a substitute for a reviewer agent that does.

A reviewer agent that opens threads and never returns to resolve them blocks the merge permanently short of that override. Opening threads sparingly is part of the same discipline — every open thread blocks a merge.

## Where these agents live

`erun-builder` and `erun-reviewer` are the two standing roles this topology names, shipped as reusable agents — see the [reusable-agent artifact spec](/agent-reference/agents-spec) for their catalogue entries, and [Agent patterns § 5](/collaboration/agent-patterns#5-review--comment--fix-loop) for the underlying API calls. The [`/erun-merge`](/agent-reference/skills-spec#erun-merge) and [`/erun-review`](/agent-reference/skills-spec#erun-review) skills these two agents drive are documented in the [skill catalogue](/agent-reference/skills-spec#built-in-skill-catalogue). Assigning a reviewer to a review from any erun client still needs [#1515](https://github.com/sophium/erun/issues/1515) — until it lands, `erun review list --waiting-on-me` already filters to reviews naming you as a reviewer — the filter exists — but nothing yet in the CLI, MCP, or desktop can *add* a reviewer to a review, so populating that list is manual today (direct API access).

## See also

- [Agent patterns](/collaboration/agent-patterns) — the underlying MCP/API call sequence this topology composes.
- [Workflow](/collaboration/workflow) — where the Build Agent / Review Agent pattern sits in the larger Operator-Agent maturity model.
- [Merge queue](/collaboration/merge-queue) — the queue this topology's `advance`/`override-advance` step drives, including recovering a wedged gate.
- [Reviews](/collaboration/reviews), [Comments](/collaboration/comments) — the API resources this topology operates over.
- [Environment types](/concepts/environment-types) — why `runtime` and `host` sit outside the builder/reviewer choice.
- [Reusable-agent spec](/agent-reference/agents-spec) — the `erun-builder` / `erun-reviewer` catalogue entries.
