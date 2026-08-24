---
title: Builds
---

# Builds

A **build** records the outcome of building a specific commit on a specific review. Builds drive review status transitions — a `READY` review is one with a successful latest build.

There are two kinds, distinguished by `kind`, and they are against different commits:

- **`RECORDED`** — a *reported* build: a client's `POST /builds` (typically `erun build --release` or your own CI, against the review's `sourceBranch` tip), or the [release queue](#release-queue)'s own build (against the merge commit, once it already landed). A `RECORDED` build always names the `version` it produced.
- **`GATE`** — the [merge queue](#merge-queue)'s own build of the *prospective* merge — the review's source squashed onto its current target, before anything is pushed. A `GATE` build publishes nothing, so it never has a `version`; a failed one carries `failureDetail` in the gate's own words.

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

A failed `GATE` build additionally carries `failureDetail` (a string, the gate's own account of why it did not succeed) instead of `version`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews/{reviewId}/builds` | List builds for a review, both kinds. |
| `POST` | `/v1/reviews/{reviewId}/builds` | Record a new `RECORDED` build. Body: `commitId`, `version`, `successful`. (`GATE` builds are written only by the merge queue itself, never by `POST`.) |
| `GET` | `/v1/reviews/{reviewId}/builds/{buildId}` | Fetch one build. |

## How builds connect to review status

After a build is recorded, the Agent that ran it typically follows up with a status update on the review:

```
POST /v1/reviews/rev_abc/builds
{ "commitId": "abc123", "version": "1.2.3", "successful": true }

→ response: { "buildId": "bld_xyz", ... }

PATCH /v1/reviews/rev_abc/status
{ "status": "READY", "buildId": "bld_xyz" }
```

The review's `lastReadyBuildId` (or `lastFailedBuildId` on failure) is updated to point at the build that triggered the transition. Other agents can `GET /v1/reviews/{reviewId}` and immediately see which build the current status reflects.

## Why builds are server-side resources

Recording builds on the server (instead of treating them as ephemeral CI artifacts) lets agents:

- Determine whether a peer's review is ready to merge without re-running the build locally.
- Look up the exact version and commit that produced a `READY` or `MERGED` state — auditable promotion.
- Reconstruct the history of attempts on a review (every `POST /builds` is appended), which is useful for both retrospective analysis and for AI agents that learn from a project's build patterns over time.

## Triggering builds

Recording a build is decoupled from running one. Any Agent or pipeline that produced a build (often `erun build --release`, or build infrastructure you already have) can call `POST /builds` once it completes, and the outcome funnels into the same review/merge-queue model.

ERun also ships its own trigger for two cases it owns end to end: gating a review's merge (the [merge queue](#merge-queue), directly below) and, once a review is `MERGED`, releasing what it merged (the [release queue](#release-queue), below).

## Merge queue

A `GATE` build is never `POST`ed — it is written by the merge queue itself once a review reaches `MERGE` (see [Reviews · Merge queue](/collaboration/reviews#merge-queue) for the promotion and dispatch workflow). Where a `RECORDED` build is against whatever commit its reporter names, a `GATE` build is always against the specific commit the merge queue built: the prospective squash merge of the review's `sourceBranch` onto its target branch as that target stood *at gate time* — not the source branch tip, and not a stale target.

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

A successful `GATE` build has no `version` (the gate publishes nothing) and no `failureDetail`; its `commitId` is the merge commit that was actually pushed to the target branch, and the review's `lastMergedBuildId` points at it. There is no PATCH to follow: the merge queue applies the review's `MERGE` → `MERGED`/`FAILED` transition itself from the gate's own outcome.

## Release queue

When a review reaches `MERGED`, the commit it merged on is enqueued for release. The queue runs `erun release` as a Kubernetes Job in an [agent env](/concepts/environment-types), records the version it minted as a build against the review, and moves the review with it.

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

| Bound | Value | Why |
|---|---|---|
| In flight per tenant | 1 | Two concurrent releases corrupt one version line. Enforced in the database, not just in the query. |
| Cooldown between consecutive releases | 60s | A trigger stuck in a loop cannot spend a tenant's capacity on back-to-back runs; negligible against a real release's duration. |
| Releases started per dispatch | 4 | Bounds fan-out across tenants. When the cap bites it is logged, never silently dropped. |
| Job deadline | 3h | Past this a release is wedged, not slow. |

A release whose control plane stops reporting for 4 hours is failed with a reason saying so, and the queue moves on — a crashed control plane never holds a tenant's slot permanently.

### Error behaviour

| Failure mode | What happens | Recovery |
|---|---|---|
| The release Job fails | `status: failed`, `failureReason` carries the run's own output (read off the pod before it is reclaimed). The next queued release still runs. | Fix the cause, re-trigger the same commit — it re-queues as a new attempt. |
| The Job exits 0 but names no version | `status: failed`. A release nothing can name is not recorded as a success. | Re-trigger the commit. |
| The queue is not configured | The trigger is still recorded as `queued`; nothing runs. | Enable the queue on the control plane, then re-trigger. |
| The review reaches `MERGED` but enqueuing its release fails | No HTTP caller to answer — the merge queue's own gate build already landed the merge asynchronously, so there is nothing left in flight to return a `500` to. The review stays `MERGED`; only the release trigger did not run. | `POST /v1/releases` with the merge commit. |

## Validation rules

| Field | Rule |
|---|---|
| `commitId` | Exactly 40 lowercase hex characters: `^[0-9a-f]{40}$`. |
| `version` | Required and must satisfy `^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$` (same grammar as [Release version policy](/agent-reference/release-policy#version-string-grammar)) — or an agent-env snapshot tag (`<semver>-snapshot-<UTC-timestamp>`) — for a `RECORDED` build. Absent for a `GATE` build, which publishes nothing. |
| `successful` | Required. Boolean. |
| `kind` | Ignored on `POST` — every client-reported build is `RECORDED`. `GATE` is written only by the merge queue. |
| `failureDetail` | Required, non-empty, on a failed `GATE` build. Not settable on `POST` (client-reported builds carry no `failureDetail`). |

## Errors

Standard HTTP semantics (see [Reviews · Errors](/collaboration/reviews#errors)). Build-specific cases:

| Status | `code` | When |
|---|---|---|
| `400` | `INVALID_COMMIT_ID` | `commitId` is not 40 lowercase hex chars. |
| `400` | `INVALID_VERSION` | `version` fails the version grammar. |
| `404` | — | The review id doesn't exist or isn't visible. |
| `422` | `UNKNOWN_COMMIT` | `commitId` doesn't exist on the review's `sourceBranch`. |

Builds are append-only — there is no `PATCH /builds/{id}` and no DELETE. Re-running a build records a new build resource; the review's `lastReadyBuildId` / `lastFailedBuildId` are updated by the subsequent `PATCH /reviews/{id}/status` call.

## Pagination + rate limits

`GET /builds` paginates at 100 items per response; see [API protocol · Pagination](/agent-reference/api-protocol#pagination). Builds share the write rate-limit bucket (60 req/min/token); see [API protocol · Rate limits](/agent-reference/api-protocol#rate-limits).
