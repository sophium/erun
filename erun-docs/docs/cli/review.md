---
title: erun review
---

# `erun review`

Review code on a hosted erun platform from a terminal or an Agent — the client for the [collaboration API](/collaboration/reviews) — using the **erun-type cloud alias** [`erun cloud init erun`](/cli/cloud) and `erun cloud login` set up. List reviews, open one, comment on a line (or reply to an existing comment), resolve or reopen a comment thread, record a build against a review (or report one MERGED once its promoted environment has gate-built and pushed it), close a review, requeue one stuck at `MERGE`, assign or remove reviewers, and inspect or advance a target branch's [merge queue](/collaboration/merge-queue).

Starting a review needs the source branch to already exist on the remote — push it first with [`erun exec push`](/cli/exec#exec-push).

The [desktop app](/desktop/reviews)'s tenant dashboard has a **Reviews** tab that covers the same ground from the app: it lists reviews, their builds and their comment threads, opens a review with **New review** (committing and pushing the environment's branch first), closes one, advances a target branch's merge queue, and starts a new comment thread from a line in the review panel's diff.

## Synopsis

```
erun review list [flags]
erun review show REVIEW_ID [flags]
erun review create --name <name> --source-branch <branch> --target-branch <branch> [flags]
erun review comment REVIEW_ID --commit <hash> --file <path> --line <n> [flags]
erun review resolve REVIEW_ID COMMENT_ID [flags]
erun review unresolve REVIEW_ID COMMENT_ID [flags]
erun review close REVIEW_ID [flags]
erun review record-build REVIEW_ID --commit <hash> --version <version> [flags]
erun review report-merged REVIEW_ID --build-id <id> --remote-url <url> [flags]
erun review requeue REVIEW_ID [flags]
erun review reviewers list REVIEW_ID [flags]
erun review reviewers add REVIEW_ID --user-id <id> [flags]
erun review reviewers remove REVIEW_ID --user-id <id> [flags]
erun review queue list --target-branch <branch> [flags]
erun review queue advance --target-branch <branch> [flags]
erun review queue override-advance --target-branch <branch> --reason <text> [flags]
```

Every subcommand accepts `--erun-alias` (defaults to the sole configured erun-type alias when only one is set up), `--dry-run` (trace the resolved HTTP call without sending it), and the global `--output json` for structured results.

## Subcommands

### `review list`

Lists reviews visible to the caller's tenant. Every filter is optional and composable.

| Flag | Description |
|---|---|
| `--target-branch` / `--source-branch` | Filter by branch name. |
| `--status` | `OPEN`, `CLOSED`, `FAILED`, `READY`, `MERGE`, or `MERGED`. |
| `--author-user-id` / `--reviewer-user-id` | Filter by an explicit user id. |
| `--mine` | Reviews you authored. Resolves your user id via a `whoami` call first; cannot be combined with `--author-user-id`. |
| `--waiting-on-me` | Reviews you are a reviewer on. Resolves your user id via a `whoami` call first; cannot be combined with `--reviewer-user-id`. |

### `review show`

Fetches one review together with its comment threads and recorded builds.

### `review create`

Opens a review. `--name` is the eventual squash-merge message and must be unique per tenant — a colliding name fails with a conflict. `--source-branch` must already be pushed (see [`exec push`](/cli/exec#exec-push)); the review references it by name and the platform can only ever fetch what has actually landed on the remote. A real, immediate write, not a preview, unless `--dry-run` is set.

### `review comment`

Comments on a line of a review, or replies to an existing comment with `--reply-to`. The comment body is read verbatim from stdin — never a shell, so nothing in it is reinterpreted, the same property [`exec commit`](/cli/exec#exec-commit) uses for its own message input.

| Flag | Description |
|---|---|
| `--commit` | Commit hash the comment is anchored to. |
| `--file` | File path the comment is anchored to. |
| `--line` | Line number the comment is anchored to. |
| `--reply-to` | Comment id to reply to, making this a reply in that thread. |

### `review resolve` / `review unresolve` {#review-resolve--review-unresolve}

Resolve a comment thread by closing its root comment, or reopen one by marking its root comment `OPEN` again. A thread's status lives entirely on its root comment — `COMMENT_ID` must be the thread's root (the first comment posted at a file/line, not one made with `--reply-to`); addressing a reply fails, naming the root comment to retry against. Only that thread's own root-comment author can resolve or reopen it — running this against someone else's thread is refused; ask that author, or see [Merge queue § Overriding the gate](/collaboration/merge-queue#overriding-the-gate) if it's blocking a merge and the author is unavailable.

### `review close`

Closes a review without merging it.

### `review record-build` {#review-record-build}

Records a build against a review — the only way an erun client transitions a review off `OPEN`. Recording a successful build moves it to `READY` (and, if it was already the merge queue's head, on to `MERGE`); recording a failed one moves it to `FAILED`. There is no separate command to set a review's status directly to `READY` or `FAILED` — only a recorded build result does that. See [Builds](/collaboration/builds) for the resource shape and validation rules.

| Flag | Description |
|---|---|
| `--commit` | Full 40-character commit hash the build ran against. |
| `--gate` | Record the merge queue's own `GATE` build kind instead of an ordinary build — set by the environment a review's merge queue promoted to `MERGE`, reporting its own build of the prospective merge. Omit `--version` when this is set: the gate publishes nothing. |
| `--version` | Version the build minted (from `erun build --release --output json`), required even for a failed build — release resolves the version before the build step runs. Omit with `--gate`. |
| `--failed` | Record the build as failed instead of successful. |
| `--failure-detail` | Why the build failed. Only meaningful with `--failed`. |

### `review report-merged` {#review-report-merged}

Reports a review `MERGED`. This is for the environment a review's merge queue promoted to `MERGE`, once it has fetched the review's target and source (see [`exec gate-merge`](/cli/exec#exec-gate-merge)), gate-built the prospective squash merge, recorded that as a successful `GATE` build (`review record-build --gate`), and pushed the result — never before the push actually landed.

The platform does not take this on trust: it checks `--build-id` names an already-recorded, successful `GATE` build for this review, then fetches `--remote-url` to confirm that build's commit is really reachable from the target branch's tip with the parent this review was gated against. Either check failing refuses with `409 Conflict` (`MERGE_NOT_VERIFIED`) and leaves the review at `MERGE`. See [Merge queue](/collaboration/merge-queue) for the full mechanics.

| Flag | Description |
|---|---|
| `--build-id` | The successful `GATE` build's id. |
| `--remote-url` | The git remote the platform fetches to verify the merge. |

### `review requeue` {#review-requeue}

Moves a review stuck at `MERGE` back to `READY`, freeing its target branch's merge-queue slot so a different review can be promoted — only one review may be at `MERGE` per target branch. This is for a review whose gate never reaches a terminal state, or one left at `MERGE` by a batched [`exec gate-merge`](/cli/exec#exec-gate-merge) whose other members landed but were never promoted (see [Merge queue § When the gate wedges](/collaboration/merge-queue#when-the-gate-wedges)). The requeued review rejoins the queue at the tail, not the head — it is not promoted again immediately.

Refuses, naming the review's actual status, when it is not at `MERGE`. Takes no `--reason`: unlike `queue override-advance`, this bypasses no safety gate — the platform already treats `MERGE -> READY` as unconditionally valid — so there is nothing to make accountable.

### `review reviewers list` / `review reviewers add` / `review reviewers remove` {#review-reviewers}

Assign or remove reviewers on a review, and list who's currently assigned. `reviewers add --user-id` must already be enrolled in your own tenant — checked before the network call, not only by the platform's own tenant-scoped refusal — and an already-assigned user is a `409 Conflict`. Assigning a reviewer makes `erun review list --waiting-on-me` return that review for them; it gates no status transition by itself — see [merge queue](/collaboration/merge-queue) for what actually blocks a merge.

### `review queue list` / `review queue advance` {#review-queue-list--review-queue-advance}

Lists or advances a target branch's merge queue. `list` returns the queue in order; `advance` promotes the queue's head to `MERGE` and starts its merge-gate build — a real build of the prospective merge, gating whether it actually lands. It fails if the queue is empty or its head is not `READY` (both surface as `404 Not Found`), or if the head still has unresolved comment threads (`409 Conflict`). On that last refusal, the command names how many threads and on which review; resolve them with [`review resolve`](#review-resolve--review-unresolve) or use `review queue override-advance`. See [Merge queue](/collaboration/merge-queue) for the full mechanics — why the queue exists, what the gate does, and how to recover a wedged gate build with [`review requeue`](#review-requeue) (see [Merge queue § When the gate wedges](/collaboration/merge-queue#when-the-gate-wedges)).

### `review queue override-advance` {#review-queue-override-advance}

Bypasses `review queue advance`'s unresolved-thread check and advances anyway. `--reason` is required and is recorded in the platform's [audit trail](/collaboration/operator-in-the-loop) alongside your identity — this is a deliberate, accountable escape hatch for a genuine exception, not a routine way to advance the queue. A tenant can grant this separately from ordinary `advance`, so it may be unavailable even to operators who can otherwise advance the queue.

## Examples

```bash
erun cloud init erun --api-url https://api.erunpaas.com
erun cloud login --alias erun+api.erunpaas.com@erun

erun exec push feature/add-widget
erun review create --name "Add widget" --source-branch feature/add-widget --target-branch main

erun review list --mine
erun review list --waiting-on-me --status OPEN

echo 'nit: rename this' | erun review comment 018f... --commit abc123 --file main.go --line 42
echo 'good catch, fixed' | erun review comment 018f... --commit abc123 --file main.go --line 42 --reply-to 018g...

erun review show 018f...
erun review resolve 018f... 018h...
erun review unresolve 018f... 018h...
erun review close 018f...

erun review record-build 018f... --commit $(git rev-parse HEAD) --version 1.2.3
erun review record-build 018f... --commit $(git rev-parse HEAD) --version 1.2.3 --failed --failure-detail "image build failed"
erun review record-build 018f... --commit $(git rev-parse HEAD) --gate
erun review report-merged 018f... --build-id 018j... --remote-url https://github.com/org/repo.git

erun review requeue 018f...

erun review reviewers add 018f... --user-id 018i...
erun review reviewers list 018f...
erun review reviewers remove 018f... --user-id 018i...

erun review queue list --target-branch main
erun review queue advance --target-branch main
erun review queue override-advance --target-branch main --reason "hotfix, reviewers unavailable"
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| No erun-type cloud alias configured. | Aborts before any network call, naming `erun cloud init erun --api-url <url>`. |
| More than one erun-type alias configured, `--erun-alias` omitted. | Aborts asking for an explicit `--erun-alias`. |
| `--mine`/`--waiting-on-me` combined with the equivalent explicit `--author-user-id`/`--reviewer-user-id` (`list`). | Aborts before any network call. |
| `create` with a `--name` that collides with an existing review. | `409 Conflict`. |
| `create` with a `--source-branch` that already has a live (non-`MERGED`/`CLOSED`) review proposing it onto the same `--target-branch`. | `409 Conflict` — see [branch uniqueness](/collaboration/reviews#author-reviewers-and-discovery). |
| `show`/`comment`/`close` on an unknown review id. | `404 Not Found`. |
| `resolve`/`unresolve` addressed to a reply rather than its thread's root comment. | Aborts before the status change, naming the root comment id to retry against. |
| `record-build` with a `--commit` that is not 40 lowercase hex characters. | `400 Bad Request` (`INVALID_COMMIT_ID`). |
| `record-build` with a `--version` that fails the version grammar. | `400 Bad Request` (`INVALID_VERSION`). |
| `record-build` on an unknown review id. | `404 Not Found`. |
| `record-build --gate --failed` whose `--failure-detail` matches a known erun infrastructure-failure signature (a registry or network giving up, not a verdict about the change). | Aborts before any network call, naming the matched signature and the remedy: report the gate run `inconclusive` via [`exec gate-run report`](/cli/exec#exec-gate-run-report) instead of recording a `FAILED` `GATE` build. |
| `report-merged` whose `--build-id` does not name a recorded, successful `GATE` build for this review. | `409 Conflict` (`MERGE_NOT_VERIFIED`); the review stays at `MERGE`. |
| `report-merged` whose build's commit is not reachable from the target branch's tip, or whose parent does not match the tip this review was gated against. | `409 Conflict` (`MERGE_NOT_VERIFIED`); the review stays at `MERGE`. |
| `requeue` on a review that is not currently at `MERGE`. | Aborts before the status change, naming the review's actual status. |
| `reviewers add --user-id` not enrolled in your own tenant. | Aborts before any network call, naming `erun platform user list`/`erun platform user enroll`. |
| `reviewers add --user-id` already assigned to the review. | `409 Conflict`. |
| `reviewers remove --user-id` not currently assigned. | `404 Not Found`. |
| `queue advance` on an empty queue, or whose head is not `READY`. | `404 Not Found`. |
| `queue advance` whose head still has unresolved comment threads. | `409 Conflict`, naming the count and the review. Resolve them or use `queue override-advance`. |
| `queue override-advance` with `--reason` omitted or blank. | Aborts before any network call. |
