---
title: Builds
---

# Builds

> For the Operator view, see [`erun review show`](/cli/review#review-show) and [`erun build`](/cli/build).

A **build** records the outcome of building a specific commit. Most builds belong to a review — those drive review status transitions, since a `READY` review is one with a successful latest build — but a build no longer *has* to: an ordinary `erun build` run (the command an Agent runs continuously, in every environment, whether or not a review exists yet) self-reports itself against the environment it ran in instead ([#1954](https://github.com/sophium/erun/issues/1954)). Every build carries exactly one of `reviewId` or `environmentId`, never both, and never neither.

There are two kinds, distinguished by `kind`, and they are against different commits:

- **`RECORDED`** — a *reported* build: a client's `POST /builds` (an ordinary `erun build` run, `erun build --release`, or your own CI, against the review's `sourceBranch` tip or the environment that built it), or a build recorded against the merge commit once a release has landed. A `RECORDED` build always names the `version` it produced, and may or may not carry a `reviewId` (see [Builds with no review](#builds-with-no-review) below).
- **`GATE`** — a report of the [merge queue](#merge-queue)'s build of the *prospective* merge — the review's source squashed onto its current target, before anything is pushed. A `GATE` build publishes nothing, so it never has a `version`; a failed one carries `failureDetail` in the gate's own words. A `GATE` build always carries a `reviewId` — it gates that review's merge by definition, so it has no unattached form.

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

`environmentId`/`environmentName` replace `reviewId`/`reviewName` on a build with no review — see [Builds with no review](#builds-with-no-review).

A failed build — `GATE` or `RECORDED` — may carry `failureDetail` (a string account of why it did not succeed). The gate always sets its own; a `RECORDED` build's reporter sets it optionally (`erun review record-build --failed --failure-detail "..."`).

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews/{reviewId}/builds` | List builds for a review, both kinds. Unpaginated — one review's own attempts are naturally bounded. |
| `POST` | `/v1/reviews/{reviewId}/builds` | Record a new build against this review. Body: `commitId`, `successful`, optional `failureDetail`, and `kind` (`RECORDED` if omitted). A `RECORDED` build also requires `version`; a `GATE` build must not carry one and must carry `failureDetail` when `successful` is `false`. [`erun review record-build`](/cli/review#review-record-build) is the CLI client for both — plain for `RECORDED`, `--gate` for `GATE`. |
| `GET` | `/v1/reviews/{reviewId}/builds/{buildId}` | Fetch one build. |
| `GET` | `/v1/builds` | List the tenant's whole build history — review-linked and unattached alike, newest first, paginated. See [Builds with no review](#builds-with-no-review). |
| `POST` | `/v1/builds` | Record a build with no review, against `environmentId`. `erun build` calls this itself; see [Builds with no review](#builds-with-no-review). |

## Builds with no review

An ordinary `erun build` run has no review to report against — most of the time, none exists yet for the branch being built. `erun build` reports itself to the erun platform after it finishes, whenever the environment has a platform alias configured (`erun cloud init erun`/`erun cloud login`): best-effort, and silently skipped when no alias is configured at all, so a local build with no control plane sees no behavior change. When an alias is configured but the report itself cannot complete (the platform is unreachable, the environment is not registered on it), the build still succeeds or fails on its own merits — the skip is only ever traced, never a build failure.

```jsonc
POST /v1/builds
{ "environmentId": "env_01H...", "commitId": "abc123def456...", "version": "1.0.0-snapshot-20260101010101", "successful": true }

→ response: { "buildId": "bld_xyz", "environmentId": "env_01H...", "environmentName": "dev", "kind": "RECORDED", ... }
```

- `environmentId` is required; there is no review-status transition to trigger (`MarkBuildResult` is skipped entirely for a build with no `reviewId`).
- `kind` must be `RECORDED` (or omitted) — `POST /v1/builds` refuses `kind: GATE` outright, since a gate build always belongs to the review it gates.
- A caller-supplied `reviewId` in the body is always ignored on this route; report a review-linked build through `POST /v1/reviews/{reviewId}/builds` instead. There is exactly one route for each shape.
- `GET /v1/builds` supports filters `environmentId`, `kind`, `successful` (`true`/`false`), `since`/`until` (RFC3339), and keyset pagination (`cursor`, `limit`, default 50, max 200) — the same shape the [audit log](/agent-reference/audit-log)'s pagination uses. The response is `{ "builds": [...], "nextCursor": "..." }`; `nextCursor` is absent on the last page. This route paginates where the review-nested `GET /v1/reviews/{reviewId}/builds` does not, because it is the platform's highest-frequency write — every `erun build` invocation, across every environment, reports here — where a review's own build history stays naturally small.

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

A successful `GATE` build has no `version` (the gate publishes nothing) and no `failureDetail`; its `commitId` is the merge commit the environment actually pushed to the target branch. Reporting it does not, by itself, move the review to `MERGED` — a failed `GATE` build does move the review straight to `FAILED` (the same as a failed `RECORDED` build would), but a successful one still needs the review moved to `MERGED` with a separate `PATCH /reviews/{id}/status`, because that is where the platform verifies the build against the real repository before accepting it (see [Merge queue § The gate](/collaboration/merge-queue#the-gate)). [`erun review report-merged`](/cli/review#review-report-merged) is the CLI client for that call. Once accepted, the review's `lastMergedBuildId` points at this build.

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
| `kind` | `RECORDED` or `GATE`; defaults to `RECORDED` when omitted. `POST /v1/builds` (no review) refuses `GATE`. |
| `failureDetail` | Required, non-empty, on a failed `GATE` build. Optional on a `RECORDED` build's `POST` — a reporter may set it on a failed build to say why; omitted is fine. |
| `reviewId` / `environmentId` | Exactly one of the two identifies a build. `POST /v1/reviews/{reviewId}/builds` sets `reviewId` from the path (any body value is ignored); `POST /v1/builds` requires `environmentId` in the body and always clears `reviewId`. |

## Errors

Same envelope and status-code conventions as [Reviews · Errors](/collaboration/reviews#errors). Build-specific cases:

| Status | `code` | When |
|---|---|---|
| `400` | `INVALID_BODY` | Malformed JSON. |
| `400` | `INVALID_COMMIT_ID` | `commitId` is not 40 lowercase hex chars. |
| `400` | `INVALID_VERSION` | `version` fails the version grammar. `RECORDED` only — a `GATE` build is never checked against it, since the gate publishes nothing. |
| `400` | `INVALID_BODY` | `kind` is neither `RECORDED` nor `GATE`; a failed `GATE` build (`successful: false`) with no `failureDetail`; `POST /v1/builds` given `kind: GATE`; or a `POST /v1/builds` call with no `environmentId`. |
| `400` | `INVALID_QUERY` | `GET /v1/builds`'s `successful` filter is not `true`/`false`, or `since`/`until` is not RFC3339, or `cursor` is malformed. |
| `404` | — (generic) | The review id doesn't exist or isn't visible to the caller's tenant. |

`422 Unprocessable Entity` and the `UNKNOWN_COMMIT` code from an earlier draft of this table do not appear above: nothing in this API returns `422`, and `UNKNOWN_COMMIT`'s original description — confirming `commitId` exists on the review's `sourceBranch` — is a check this route cannot perform (an ordinary `RECORDED` report carries no remote to fetch). A `MERGED` report is the one place the API does fetch the real repository to verify a commit — see [Reviews · Machine error codes](/collaboration/reviews#machine-error-codes)'s `MERGE_NOT_VERIFIED`. `PATCH /reviews/{id}/status` referencing a `buildId` that doesn't belong to the review, or whose `successful` flag disagrees with the target status, is a plain `404` — see [Reviews · Errors](/collaboration/reviews#errors).

Builds are append-only — there is no `PATCH /builds/{id}` and no DELETE. Re-running a build records a new build resource; the review's `lastReadyBuildId` / `lastFailedBuildId` are updated by the subsequent `PATCH /reviews/{id}/status` call.

## Pagination + rate limits

`GET /v1/reviews/{reviewId}/builds` returns every build on the review in one response, unpaginated — one review's own attempts stay small enough that this is not a problem. `GET /v1/builds` (the tenant-wide history) does paginate, with the same keyset-cursor shape as the [audit log](/agent-reference/audit-log)'s: `cursor`/`limit` request params, `nextCursor` in the response, absent on the last page. See [API protocol · Pagination](/agent-reference/api-protocol#pagination). No request is refused for rate; see [API protocol · Rate limits](/agent-reference/api-protocol#rate-limits) for the target design.

Retention for the tenant-wide build history is intentionally not part of this contract yet — see [#1956](https://github.com/sophium/erun/issues/1956): the table has no TTL or row cap today, the same as every other high-volume table on the platform (`usage_events`, `gate_runs`, `audit_events`).
