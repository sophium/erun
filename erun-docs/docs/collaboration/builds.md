---
title: Builds
---

# Builds

> For the Operator view, see [`erun review show`](/cli/review#review-show).

A **build** records the outcome of building a specific commit on a specific review. Builds drive review status transitions — a `READY` review is one with a successful latest build.

There are two kinds, distinguished by `kind`, and they are against different commits:

- **`RECORDED`** — a *reported* build: a client's `POST /builds` (typically `erun build --release` or your own CI, against the review's `sourceBranch` tip), or a build recorded against the merge commit once a release has landed. A `RECORDED` build always names the `version` it produced.
- **`GATE`** — a report of the [merge queue](#merge-queue)'s build of the *prospective* merge — the review's source squashed onto its current target, before anything is pushed. A `GATE` build publishes nothing, so it never has a `version`; a failed one carries `failureDetail` in the gate's own words.

## Resource shape

```jsonc
{
  "buildId": "bld_01H...",
  "tenantId": "tnt_01H...",
  "reviewId": "rev_01H...",
  "reviewName": "Refactor pricing engine",   // read-only display field
  "kind": "RECORDED",                       // RECORDED | GATE
  "successful": true,
  "commitId": "abc123def456",
  "version": "1.2.3",                       // absent for a GATE build
  "createdAt": "2026-05-24T11:13:00Z",
  "updatedAt": "2026-05-24T11:13:00Z"
}
```

A failed build — `GATE` or `RECORDED` — may carry `failureDetail` (a string account of why it did not succeed). The gate always sets its own; a `RECORDED` build's reporter sets it optionally (`erun review record-build --failed --failure-detail "..."`).

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews/{reviewId}/builds` | List builds for a review, both kinds. |
| `POST` | `/v1/reviews/{reviewId}/builds` | Record a new build. Body: `commitId`, `successful`, optional `failureDetail`, and `kind` (`RECORDED` if omitted). A `RECORDED` build also requires `version`; a `GATE` build must not carry one and must carry `failureDetail` when `successful` is `false`. [`erun review record-build`](/cli/review#review-record-build) is the CLI client for the `RECORDED` case. |
| `GET` | `/v1/reviews/{reviewId}/builds/{buildId}` | Fetch one build. |

## How builds connect to review status

Recording a `RECORDED` build is the transition, not a precursor to one: `POST /builds` moves the review to `READY` (successful) or `FAILED` (not) as part of the same write, and on to `MERGE` if it was already the merge queue's head. There is no separate `PATCH /reviews/{id}/status` call to make afterward — `erun review record-build` (and the `review_record-build` MCP tool) is the only way an erun client transitions a review off `OPEN`.

```
POST /v1/reviews/rev_abc/builds
{ "commitId": "abc123def456...", "version": "1.2.3", "successful": true }

→ response: { "buildId": "bld_xyz", ... }
→ review rev_abc is now READY, lastReadyBuildId = bld_xyz
```

The review's `lastReadyBuildId` (or `lastFailedBuildId` on failure) is updated to point at the build that triggered the transition. Other agents can `GET /v1/reviews/{reviewId}` and immediately see which build the current status reflects. `PATCH /reviews/{id}/status` still exists (see [Reviews](/collaboration/reviews)) but is for closing a review, not for reacting to a build.

## Why builds are server-side resources

Recording builds on the server (instead of treating them as ephemeral CI artifacts) lets agents:

- Determine whether a peer's review is ready to merge without re-running the build locally.
- Look up the exact version and commit that produced a `READY` or `MERGED` state — auditable promotion.
- Reconstruct the history of attempts on a review (every `POST /builds` is appended), which is useful for both retrospective analysis and for AI agents that learn from a project's build patterns over time.

## Triggering builds

Recording a build is decoupled from running one. Any Agent or pipeline that produced a build (often `erun build --release`, or build infrastructure you already have) can call `POST /builds` once it completes, and the outcome funnels into the same review/merge-queue model.

Two cases ERun's own clients drive end to end this same way: gating a review's merge (the [merge queue](#merge-queue), directly below) and, once a review is `MERGED`, releasing what it merged (the [release queue](#release-queue), below). Neither runs inside this API — the environment that was promoted to `MERGE`, or that earned the release, runs the real work itself (its own workspace, its own daemon) and reports the outcome through the same `POST /builds` route everything else uses.

## Merge queue

A `GATE` build is `POST`ed by whichever environment ran the merge queue's gate for a review sitting at `MERGE` (see [Merge queue § The gate](/collaboration/merge-queue#the-gate)). Where a `RECORDED` build is against whatever commit its reporter names, a `GATE` build is always against the specific commit the gate built: the prospective squash merge of the review's `sourceBranch` onto its target branch as that target stood *at gate time* — not the source branch tip, and not a stale target.

```jsonc
{
  "buildId": "bld_01H...",
  "reviewId": "rev_01H...",
  "kind": "GATE",
  "successful": false,
  "commitId": "9f1c2b3d4e5f60718293a4b5c6d7e8f901234567",   // the attempted merge commit, or the source tip if the merge itself conflicted
  "failureDetail": "CONFLICT (content): Merge conflict in pricing.go",
  "createdAt": "2026-08-24T09:12:44Z",
  "updatedAt": "2026-08-24T09:12:44Z"
}
```

A successful `GATE` build has no `version` (the gate publishes nothing) and no `failureDetail`; its `commitId` is the merge commit the environment actually pushed to the target branch. Reporting it does not, by itself, move the review to `MERGED` — a failed `GATE` build does move the review straight to `FAILED` (the same as a failed `RECORDED` build would), but a successful one still needs the review moved to `MERGED` with a separate `PATCH /reviews/{id}/status`, because that is where the platform verifies the build against the real repository before accepting it (see [Merge queue § The gate](/collaboration/merge-queue#the-gate)). Once accepted, the review's `lastMergedBuildId` points at this build.

## Release queue

When a review reaches `MERGED`, the commit it merged on is enqueued for release: `POST /v1/releases` records the trigger and holds the idempotency contract below. Running `erun release` is the same environment-driven model as the merge queue's gate — whichever environment runs it records the version it minted as a `RECORDED` build against the review once it completes.

The queue runs **one release at a time per tenant**, first in, first out. This is not a throughput choice: `release` bumps a semver, writes version-bearing files, tags, and pushes, so two concurrent releases on one version line corrupt it. Two reviews accepted seconds apart produce two sequential releases, never a race.

### Resource shape

```json
{
  "releaseId": "018f3a2b-7c41-7e93-b8d2-1f9c4e5a6b70",
  "tenantId": "018f3a2b-1111-7e93-b8d2-1f9c4e5a6b70",
  "reviewId": "018f3a2b-2222-7e93-b8d2-1f9c4e5a6b70",
  "targetBranch": "main",
  "commitId": "9f1c2b3d4e5f60718293a4b5c6d7e8f901234567",
  "status": "released",
  "attempt": 1,
  "version": "1.4.2",
  "buildId": "018f3a2b-3333-7e93-b8d2-1f9c4e5a6b70",
  "createdAt": "2026-08-16T09:12:44Z",
  "updatedAt": "2026-08-16T09:31:07Z"
}
```

| Field | Meaning |
|---|---|
| `reviewId` | The accepted review that earned the release. Absent when a merge to the target branch was triggered directly. |
| `commitId` | The merge commit being released. Unique per tenant — see [Idempotency](#idempotency). |
| `status` | `queued` → `running` → `released` \| `failed`. |
| `attempt` | Incremented each time a failed release is re-queued. Keys the Job and the durable workflow, so a retry runs instead of replaying. |
| `version` | The version the release published. Set only on `released`; a run that failed published nothing. |
| `buildId` | The build recorded against the review for this release. |
| `failureReason` | Present on `failed`: the release's own output, not "the job exited". |

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/releases` | Enqueue a release for a merge commit. `201` when queued, `200` when the commit already has one. |
| `GET` | `/v1/releases` | List releases. Filter with `targetBranch`, `reviewId`, `status`. |
| `GET` | `/v1/releases/{releaseId}` | Read one release. |
| `GET` | `/v1/reviews/{reviewId}/releases` | The releases a review produced — where a failed release's reason lives. |

`POST /v1/releases` takes `{ "reviewId": "...", "targetBranch": "main", "commitId": "<40 hex>" }`; `reviewId` is optional.

### Idempotency

There is at most one release per `(tenant, commitId)`, so **minting a second version for one merge commit cannot happen**. Re-triggering the same commit answers `200` with the row that already exists:

| Existing status | What a repeat trigger does |
|---|---|
| `released` | Nothing. The response names the version already minted for that commit. |
| `queued` / `running` | Nothing. The response is the release already in flight. |
| `failed` | Re-queues the same row with `attempt` incremented, so a transient failure never locks a commit out. |

### Bounds

The queue's serialisation rules — one running release per tenant, a cooldown between consecutive releases, and requeuing a failed attempt as a new one — are enforced as database contracts (a partial unique index on running rows, an idempotency key on `(tenant, commitId)`), so they hold regardless of what actually claims and runs a release. What claims a release and runs `erun release` for it is `(Planned.)`: releasing is environment-driven now, the same shift the merge queue's gate made (see [Merge queue § The gate](/collaboration/merge-queue#the-gate)), and no dedicated Job stands in for it — but the environment-driven replacement is not wired up yet. Today, `POST /v1/releases` reliably records the trigger; nothing drains the queue behind it.

### Error behaviour

| Failure mode | What happens | Recovery |
|---|---|---|
| Nothing runs the queued release `(Planned.)` | `status: queued` indefinitely — recorded, not run. | Run `erun release` yourself against the commit and `POST /builds` the outcome (`kind: "RECORDED"`, the `version` it minted), or trigger it again once environment-driven release execution lands. |
| The review reaches `MERGED` but enqueuing its release fails | No HTTP caller to answer — the review's own `MERGED` transition already completed, so there is nothing left in flight to return a `500` to. The review stays `MERGED`; only the release trigger did not run. | `POST /v1/releases` with the merge commit. |

## Validation rules

| Field | Rule |
|---|---|
| `commitId` | Exactly 40 lowercase hex characters: `^[0-9a-f]{40}$`. |
| `version` | Required and must satisfy `^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$` (same grammar as [Release version policy](/agent-reference/release-policy#version-string-grammar)) — or an agent-env snapshot tag (`<semver>-snapshot-<UTC-timestamp>`) — for a `RECORDED` build. Absent for a `GATE` build, which publishes nothing. |
| `successful` | Required. Boolean. |
| `kind` | `RECORDED` or `GATE`; defaults to `RECORDED` when omitted. |
| `failureDetail` | Required, non-empty, on a failed `GATE` build. Optional on a `RECORDED` build's `POST` — a reporter may set it on a failed build to say why; omitted is fine. |

## Errors

Same envelope and status-code conventions as [Reviews · Errors](/collaboration/reviews#errors). Build-specific cases:

| Status | `code` | When |
|---|---|---|
| `400` | `INVALID_BODY` | Malformed JSON. |
| `400` | `INVALID_COMMIT_ID` | `commitId` is not 40 lowercase hex chars. |
| `400` | `INVALID_VERSION` | `version` fails the version grammar. `RECORDED` only — a `GATE` build is never checked against it, since the gate publishes nothing. |
| `400` | `INVALID_BODY` | `kind` is neither `RECORDED` nor `GATE`; or a failed `GATE` build (`successful: false`) with no `failureDetail`. |
| `404` | — (generic) | The review id doesn't exist or isn't visible to the caller's tenant. |

`422 Unprocessable Entity` and the `UNKNOWN_COMMIT` code from an earlier draft of this table do not appear above: nothing in this API returns `422`, and `UNKNOWN_COMMIT`'s original description — confirming `commitId` exists on the review's `sourceBranch` — is a check this route cannot perform (an ordinary `RECORDED` report carries no remote to fetch). A `MERGED` report is the one place the API does fetch the real repository to verify a commit — see [Reviews · Machine error codes](/collaboration/reviews#machine-error-codes)'s `MERGE_NOT_VERIFIED`. `PATCH /reviews/{id}/status` referencing a `buildId` that doesn't belong to the review, or whose `successful` flag disagrees with the target status, is a plain `404` — see [Reviews · Errors](/collaboration/reviews#errors).

Builds are append-only — there is no `PATCH /builds/{id}` and no DELETE. Re-running a build records a new build resource; the review's `lastReadyBuildId` / `lastFailedBuildId` are updated by the subsequent `PATCH /reviews/{id}/status` call.

## Pagination + rate limits

Neither is implemented yet. `GET /builds` returns every build on the review in one response — see [API protocol · Pagination](/agent-reference/api-protocol#pagination) — and no request is refused for rate; see [API protocol · Rate limits](/agent-reference/api-protocol#rate-limits) for the target design.
