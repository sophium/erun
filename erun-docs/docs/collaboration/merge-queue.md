---
title: Merge queue
---

# Merge queue

The merge queue is the only path to `MERGED` — it is what makes two independently-green reviews that break the target branch together impossible, and it carries the one audited escape hatch in the product. It also has a lot of surface: a shape, a gate, a comment-thread check, three clients that can advance it, and a wedge-recovery path. This page is the single account of all of it; [Reviews](/collaboration/reviews) stays the wire-level endpoint reference, [`erun review`](/cli/review) the CLI reference, and the [desktop reviews tab](/desktop/reviews) the app reference — each links here for the mechanics rather than repeating them.

For the standing builder/reviewer roles that drive a review through this queue, see [Review loop topology](/collaboration/review-loop-topology). Getting a reviewer into that role in the first place — assigning or removing one from any client — is [`erun review reviewers`](/cli/review#review-reviewers) (also `review_reviewers_*` over MCP); see [Reviews § Author, reviewers, and discovery](/collaboration/reviews#author-reviewers-and-discovery) for the resource itself.

## Why it exists

Two reviews can each be green on their own and still break the target branch when both land — the second one was only ever tested against a target branch snapshot that the first hadn't touched yet. The merge queue closes that gap by serialising `READY` reviews per target branch: the second review promoted onto a branch is always gated against whatever the first one just landed, never against a stale snapshot.

## Shape of the queue

The queue is **shared per target branch**, not global. Every `READY` review for a given `targetBranch` waits in a single FIFO — `GET /v1/reviews/merge-queue?targetBranch=main` lists it in order. A review that has been promoted (status `MERGE`) has already left that waiting line; only one review per target branch may be `MERGE` at a time, so the review currently being gated and the reviews still waiting are always disjoint sets.

## The gate {#the-gate}

Promoting the head of the queue does real work, not a status flip — but the platform is not the one doing that work. The environment that gets promoted to `MERGE` is expected to fetch its target and source branches itself, build the prospective squash merge of the source onto the *current* target, gate that build with a real build (`erun build`), and push only if it passes: the same workspace, daemon, and warm caches it already has, rather than a separate Job standing up a cold one. `MERGE` is still reached only by promotion (`PATCH .../status` asserting `MERGE` directly is always refused).

`MERGED`, though, is not privileged to a particular caller — any caller may report it, because the platform verifies it rather than trusting who sent it. Before accepting a `PATCH .../status` with `{"status": "MERGED", "buildId": "...", "remoteUrl": "..."}`, it checks all three of:

1. **The build is real.** `buildId` names a `GATE`-kind build already recorded against this exact review, and it succeeded — a caller cannot assert `MERGED` off a build that failed, belongs to a different review, or doesn't exist.
2. **The commit is really there.** Fetching `remoteUrl`, the platform confirms the build's `commitId` is genuinely reachable from the tip of the review's target branch — not just a commit the caller says it made.
3. **It was built on the right base.** The commit's own parent has to match the target tip this review was gated against — the merge commit of whichever review most recently reached `MERGED` on the same target branch (or, for the first merge through the queue on a branch, nothing to compare against yet). A merge computed against a target that had already moved on is refused even though the commit it produced is genuinely on the branch.

All three have to hold, or the transition is refused with `409 Conflict` and code `MERGE_NOT_VERIFIED` (see [Reviews § Machine error codes](/collaboration/reviews#machine-error-codes)) — nothing about the review changes. This is a strictly stronger guarantee than trusting a privileged caller: it is a fact about the repository, checkable by fetching the same remote yourself, not a claim believed because of who reported it.

The gate's build is recorded as a [`GATE`-kind build](/collaboration/builds#merge-queue) via the ordinary `POST /builds` route: it publishes nothing, so it carries no `version`, and a failed one carries `failureDetail` in the gate's own words. A successful gate's build becomes the review's `lastMergedBuildId` once `MERGED` is accepted.

The client tooling for this side is [`erun exec gate-merge`](/cli/exec#exec-gate-merge) (fetch and squash-merge onto a fresh checkout of the target), [`erun review record-build --gate`](/cli/review#review-record-build) (record the `GATE` build, successful or failed), and [`erun review report-merged`](/cli/review#review-report-merged) (report `MERGED` once the push actually lands — refused with `MERGE_NOT_VERIFIED` otherwise). The `erun-merge-queue-drive` skill chains all three for a review a promotion already targeted; nothing polls for a promotion and runs it automatically today, so it is invoked explicitly, the same way `advance`/`override-advance` below are.

A repository merged through a plain GitHub pull request instead of an erun review — never calling `MERGE`/`MERGED` at all — can still require this same gate build via GitHub's own branch protection: [`erun exec report-commit-status`](/cli/exec#exec-report-commit-status) turns the gate build's outcome into a GitHub commit status on the pull request's head commit, which a required-status-checks rule can then require before GitHub allows the merge. This is a separate mechanism from the `MERGE`/`MERGED` verification above — it never touches an erun review at all — but reuses the same gate build a `gate-merge` + real build already produced.

## The unresolved-thread check {#the-unresolved-thread-check}

Before promoting the head review, the queue checks its comment threads. If any thread is still `OPEN` (its root comment unresolved), advancing refuses with `409 Conflict` and a structured body — the one place on this API that uses its own bespoke shape instead of the standard `{code, message, details}` envelope, naming the count and the review so a caller can act on it instead of parsing a sentence:

```jsonc
{
  "error": "unresolved_threads",
  "message": "review rev_01H... has 3 unresolved comment thread(s); resolve them before advancing the merge queue",
  "reviewId": "rev_01H...",
  "unresolvedThreads": 3
}
```

Clearing it takes one of two things: resolve the threads, or use [`override-advance`](#overriding-the-gate). Resolving a thread is itself restricted — **only that thread's own root-comment author can close it** (see [Comments § Open / closed](/collaboration/comments#comment-status)). The builder that opened the review cannot resolve a reviewer's thread no matter how completely it addressed the point; if the reviewer never comes back, the review is stuck behind that thread short of an override. This is deliberate, not an oversight — see [Review loop topology § The reviewer must come back](/collaboration/review-loop-topology#the-reviewer-must-come-back) for why the loop is designed around it.

## Advancing it {#advancing-it}

All three clients do the same thing: promote the queue's current head to `MERGE`. The response in every case is the *promoted* review, not the merged one — the promoted environment is expected to build, push, and report the gate itself (see [The gate](#the-gate)); poll for the terminal `MERGED` or `FAILED` outcome.

### From the API

```
POST /v1/reviews/merge-queue/advance
Content-Type: application/json

{ "targetBranch": "main" }
```

See [Reviews § Endpoints](/collaboration/reviews#endpoints) for the full request/response shape.

### From the CLI

```bash
erun review queue advance --target-branch main
```

See [`erun review queue advance`](/cli/review#review-queue-list--review-queue-advance).

### From the desktop

Tenant dashboard → a review's detail → **Merge queue** tab → **Advance queue**, behind a confirm step. The action is replaced by the missing-access affordance when your account can't use it. See [Desktop reviews § Merge queue and comment threads](/desktop/reviews#merge-queue-and-comment-threads).

## Overriding the gate {#overriding-the-gate}

`override-advance` promotes the head exactly as `advance` does, but skips the unresolved-thread check:

```
POST /v1/reviews/merge-queue/override-advance
Content-Type: application/json

{ "targetBranch": "main", "reason": "hotfix, reviewers unavailable" }
```

`reason` is required — blank or missing is refused with `400 Bad Request` before anything is promoted — and is recorded in the [audit trail](/agent-reference/audit-log) alongside the caller's identity, as an `API`-type event whose `apiPath` is `/v1/reviews/merge-queue/override-advance`. This is the one legitimate escape from the thread gate: deliberate (a distinct call, not a flag on `advance`), accountable (reason + caller both durably recorded), and separately authorized — a tenant can grant `advance` without granting `override-advance`, since they're different API paths.

- **CLI:** `erun review queue override-advance --target-branch main --reason "hotfix, reviewers unavailable"` — see [`erun review queue override-advance`](/cli/review#review-queue-override-advance).
- **Desktop:** same **Advance queue** action, available only to an account with the separate override permission; it bypasses the thread-count refusal by asking for a reason inline.

## When the gate wedges {#when-the-gate-wedges}

If a `MERGE` review's gate never reaches a terminal state — an operator-diagnosed stuck run — requeuing it needs a direct API call today:

```
PATCH /v1/reviews/{reviewId}/status
Content-Type: application/json

{ "status": "READY" }
```

Omitting `buildId` on a `READY` transition is what marks this as the missed-merge-window path rather than a build result: the review moves back to `READY` and rejoins its target branch's queue **at the tail**, not the head — it does not get promoted again immediately. There is no CLI flag or desktop button for this yet; until one exists, an operator (or an Agent with API access) makes this call directly.

## Failure table {#failure-table}

| Refusal | HTTP status | Body | How to unblock |
|---|---|---|---|
| No `READY` review waiting for that target branch (queue empty) | `404 Not Found` | `{code: "EMPTY_QUEUE", message}` (no `details`) | Wait for a review to reach `READY` — its build succeeded — then advance again. |
| Another review is already `MERGE` for that target branch | `404 Not Found` | `{code: "NOT_FOUND", message}` (no `details`) | Wait for it to reach `MERGED`/`FAILED`, or see [When the gate wedges](#when-the-gate-wedges) if it looks stuck. |
| The head review has an unresolved comment thread | `409 Conflict` | structured — `{error, message, reviewId, unresolvedThreads}`, shown [above](#the-unresolved-thread-check) | Resolve the thread (its root author only), or [`override-advance`](#overriding-the-gate). |

The first two rows both 404, but are distinguishable now: [Reviews · Machine error codes](/collaboration/reviews#machine-error-codes) names `EMPTY_QUEUE` for the first case (nothing `READY` waiting), while the second — another review already merging — falls through to the generic `NOT_FOUND` code every 404 gets by default, since that case has no business-specific code of its own.

## See also

- [Reviews](/collaboration/reviews) — the review resource, its status lifecycle, and the merge-queue endpoints' wire contract.
- [Comments](/collaboration/comments) — thread status and the root-author-only close rule the unresolved-thread check depends on.
- [Builds](/collaboration/builds) — the `GATE` build kind the gate writes.
- [`erun review`](/cli/review) — the CLI client.
- [Desktop reviews](/desktop/reviews) — the desktop client.
- [Review loop topology](/collaboration/review-loop-topology) — the builder/reviewer roles that drive a review through this queue.
- [Workflow](/collaboration/workflow) — where `MERGE`/`MERGED` sit in the larger `PR`/`QA`/`DONE` mapping.
