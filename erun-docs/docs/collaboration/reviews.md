---
title: Reviews
---

# Reviews

> For the Operator view, see [`erun review`](/cli/review).

A **review** is the unit of work-to-be-merged. It binds a source branch to a target branch and tracks the state of that pairing through to merge.

## Resource shape

```jsonc
{
  "reviewId": "rev_01H...",
  "tenantId": "tnt_01H...",
  "authorUserId": "usr_01H...",           // the caller that created the review; server-derived, never client-set
  "name": "Refactor pricing engine",
  "targetBranch": "main",
  "sourceBranch": "feature-a",
  "status": "OPEN",                       // OPEN | CLOSED | FAILED | READY | MERGE | MERGED
  "lastFailedBuildId": "bld_...",
  "lastReadyBuildId": "bld_...",
  "lastMergedBuildId": "bld_...",
  "createdAt": "2026-05-24T10:42:00Z",
  "updatedAt": "2026-05-24T11:13:00Z"
}
```

`authorUserId` is always the authenticated caller that created the review. A `POST /v1/reviews` body that includes `authorUserId` has it ignored — a client cannot assert authorship for someone else.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews` | List reviews. Optional filters, all composable: `?targetBranch=<name>`, `?sourceBranch=<name>`, `?status=<OPEN\|CLOSED\|FAILED\|READY\|MERGE\|MERGED>`, `?authorUserId=<id>`, `?reviewerUserId=<id>`. |
| `POST` | `/v1/reviews` | Create a review. Body: `name`, `sourceBranch`, `targetBranch`. Refused with `409 Conflict` if another non-`MERGED`/`CLOSED` review already proposes the same `sourceBranch` onto the same `targetBranch`. |
| `GET` | `/v1/reviews/{reviewId}` | Fetch one review. |
| `PATCH` | `/v1/reviews/{reviewId}/status` | Update review status. Body: `status`, `buildId`. |
| `GET` | `/v1/reviews/merge-queue` | List reviews *waiting* to merge for a target branch (status `READY`, not yet promoted). Optional `?targetBranch=<name>`. |
| `POST` | `/v1/reviews/merge-queue/advance` | Promote the next waiting review to `MERGE` and dispatch its merge-queue gate. Body: `targetBranch`. Refuses with `409 Conflict` when the head still has unresolved comment threads — see [Merge queue](#merge-queue). Does not itself produce `MERGED`. |
| `POST` | `/v1/reviews/merge-queue/override-advance` | Bypass the unresolved-thread refusal and advance anyway. Body: `targetBranch`, `reason`. A distinct, separately-authorized route — see [Overriding the gate](#overriding-the-gate). |
| `GET` | `/v1/reviews/{reviewId}/reviewers` | List the review's reviewers. |
| `POST` | `/v1/reviews/{reviewId}/reviewers` | Add a reviewer. Body: `userId`. |
| `DELETE` | `/v1/reviews/{reviewId}/reviewers/{userId}` | Remove a reviewer. `204 No Content` on success. |

## Author, reviewers, and discovery

Every review has exactly one author and any number of reviewers:

- **Author.** Set once, at creation, to the authenticated caller. It never changes and cannot be reassigned.
- **Reviewers.** Zero or more users explicitly assigned to a review. Assigning a reviewer does not gate any status transition today — `PATCH .../status` still works the same regardless of who (if anyone) is assigned. A reviewer's tenant must match the review's tenant; a cross-tenant `userId` is refused.

The reviewer resource:

```jsonc
{
  "tenantId": "tnt_01H...",
  "reviewId": "rev_01H...",
  "userId": "usr_01H...",
  "createdAt": "2026-05-24T10:42:00Z",
  "updatedAt": "2026-05-24T11:13:00Z"
}
```

The `authorUserId` and `reviewerUserId` list filters make two questions answerable directly, without client-side filtering: "my reviews" is `GET /v1/reviews?authorUserId=<me>`, and "reviews waiting on me" is `GET /v1/reviews?reviewerUserId=<me>`. Both compose with `status`, `targetBranch`, and `sourceBranch`.

## One live review per branch pair

At most one non-`MERGED`, non-`CLOSED` review may propose a given `sourceBranch` onto a given `targetBranch` at a time. `POST /v1/reviews` for a branch pair that already has a live review fails with `409 Conflict`. Once that review reaches `MERGED` or `CLOSED`, the same branch pair can be proposed again — branch history is unbounded, only *live* duplicates are refused. This prevents two reviews from independently reaching the merge queue for the same change, where the second would merge a branch the target already contains.

## Status lifecycle

```mermaid
stateDiagram-v2
    classDef endpoint fill:#0f1320,color:#ffffff,stroke:#0a1019,stroke-width:1px
    classDef step fill:#ffffff,color:#0f1320,stroke:#0891b2,stroke-width:1.5px

    [*] --> OPEN: POST /reviews
    OPEN --> FAILED: failed build
    OPEN --> READY: successful build
    FAILED --> READY: fix + build
    READY --> MERGE: merge-queue/advance
    MERGE --> MERGED: gate build passes
    MERGE --> FAILED: gate build fails
    MERGE --> READY: missed merge window
    OPEN --> CLOSED: abandon
    READY --> CLOSED
    FAILED --> CLOSED
    MERGED --> [*]
    CLOSED --> [*]

    class OPEN,FAILED,READY,MERGE step
    class MERGED,CLOSED endpoint
```

The status transitions are enforced server-side: an Agent cannot directly mark a review `MERGE` or `MERGED` — `PATCH .../status` refuses both. The only path to `MERGED` is the merge queue's own gate: once `merge-queue/advance` promotes a review to `MERGE`, the queue builds the prospective merge of the review's source onto its *current* target branch, gates that build with a real build, and pushes only on green. `MERGED` means that gate actually ran and actually passed, not that any caller asserted it.

## Status meanings

| Status | What it means |
|---|---|
| `OPEN` | Review exists, no successful build yet, no decision either way. |
| `FAILED` | The latest build for this review failed. The corresponding build id is stored in `lastFailedBuildId`. |
| `READY` | The latest build succeeded; the review is mergeable. |
| `MERGE` | Promoted to the head of its target branch's queue; the merge queue is building the prospective merge and gating it with a build right now. |
| `MERGED` | The gate build passed and the merge was pushed to the target branch. Terminal. `lastMergedBuildId` records the gate build (kind `GATE`; it publishes nothing, so it carries no version — see [Builds](/collaboration/builds)). |
| `CLOSED` | Closed without merge (abandoned). Terminal. |

## Merge queue

`GET /v1/reviews/merge-queue` and `POST /v1/reviews/merge-queue/advance` (and its `override-advance` counterpart) above are the wire contract for the merge queue. For why it exists, the queue's shape, what the gate does, the unresolved-thread check's structured body, advancing and overriding it from every client, and recovering a wedged gate — see **[Merge queue](/collaboration/merge-queue)**.

### Overriding the gate {#overriding-the-gate}

See [Merge queue § Overriding the gate](/collaboration/merge-queue#overriding-the-gate).

## Errors

Endpoints return a plain-text error body by default (the HTTP status text). The one structured exception today is the merge queue's unresolved-thread refusal — see [Merge queue § The unresolved-thread check](/collaboration/merge-queue#the-unresolved-thread-check) for its JSON shape. *Machine error codes* below is this API's target `code`/`details` contract for the rest of these cases — it is not yet wired into any response, the one exception above aside.

```jsonc
{
  "code": "INVALID_TRANSITION",
  "message": "Cannot transition review from OPEN directly to MERGED",
  "details": { "from": "OPEN", "to": "MERGED", "validTargets": ["FAILED", "READY", "CLOSED"] }
}
```

| Status | When | Example |
|---|---|---|
| `400 Bad Request` | Malformed JSON, missing required fields, type mismatches; a caller asserting `MERGE` or `MERGED` directly; `override-advance` with a blank or missing `reason`. | `POST /v1/reviews` without `sourceBranch`; `PATCH .../status` with `{"status": "MERGED"}` — that status is written only by the merge queue's own gate result; `override-advance` with `reason` omitted. |
| `401 Unauthorized` | No `Authorization` header, or token validation failed. | Bearer token expired. |
| `403 Forbidden` | Token valid; caller not allowed in this tenant. | Agent of tenant A calling on tenant B. |
| `404 Not Found` | The review or build id doesn't exist or isn't visible to the caller; `merge-queue/advance` against a target branch with nothing waiting to promote — see [Merge queue § Failure table](/collaboration/merge-queue#failure-table). | `GET /v1/reviews/rev_unknown`; `POST /v1/reviews/merge-queue/advance` on an empty queue, or while another review is already `MERGE` for that target branch. |
| `409 Conflict` | Invalid state transition; a second live review for a branch pair already proposed by a live review; a reviewer already assigned to the review; the queue head has unresolved comment threads. | `POST /v1/reviews` proposing `feature-a` onto `main` while another live review already does; `advance` against a head review with an open thread — see [Merge queue](/collaboration/merge-queue#the-unresolved-thread-check) for that response's structured body. |
| `422 Unprocessable Entity` | Structurally valid but semantically invalid. | `PATCH status` to `READY` without a successful build. |
| `429 Too Many Requests` | Rate limit exceeded. | Burst of `POST /comments`. |
| `500 Internal Server Error` | Server error. Retry. | Database unavailable. |

### Machine error codes

| `code` | When | HTTP status |
|---|---|---|
| `INVALID_TRANSITION` | `PATCH /status` with a transition not allowed by the [Status lifecycle](#status-lifecycle). `details.validTargets` lists the allowed next statuses. | `409` |
| `EMPTY_QUEUE` | `POST /merge-queue/advance` against a target branch whose queue has no `READY` reviews waiting — a review already `MERGE` has left that waiting line, so its presence is the separate "another review already merging" case, not this one. | `404` |
| `UNKNOWN_COMMIT` | `PATCH /status` (or `POST /builds`) referencing a `buildId` whose `commitId` doesn't exist on the review's `sourceBranch`. | `422` |
| `INVALID_BODY` | Request body missing required field or fails type validation. `details.field` names the offender. | `400` |
| `INVALID_TARGET_BRANCH` | `targetBranch` is not a valid branch name. | `400` |
| `EXPIRED_PAGE_TOKEN` | `pageToken` is stale or malformed. | `400` |

### Pagination + rate limits

List endpoints page at 100 items max; see [API protocol · Pagination](/agent-reference/api-protocol#pagination). Rate-limit buckets per token: read 600 req/min, write 60 req/min, merge-queue-advance 10 req/min. Full table: [API protocol · Rate limits](/agent-reference/api-protocol#rate-limits).
