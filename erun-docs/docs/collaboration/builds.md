---
title: Builds
---

# Builds

A **build** records the outcome of building a specific commit on a specific review. Builds drive review status transitions — a `READY` review is one with a successful latest build.

## Resource shape

```jsonc
{
  "buildId": "bld_01H...",
  "tenantId": "tnt_01H...",
  "reviewId": "rev_01H...",
  "reviewName": "Refactor pricing engine",   // read-only display field
  "successful": true,
  "commitId": "abc123def456",
  "version": "1.2.3",
  "createdAt": "2026-05-24T11:13:00Z",
  "updatedAt": "2026-05-24T11:13:00Z"
}
```

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews/{reviewId}/builds` | List builds for a review. |
| `POST` | `/v1/reviews/{reviewId}/builds` | Record a new build. Body: `commitId`, `version`, `successful`. |
| `GET` | `/v1/reviews/{reviewId}/builds/{buildId}` | Fetch one build. |

## How builds connect to review status

After a build is recorded, the agent that ran it typically follows up with a status update on the review:

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

Recording a build is decoupled from running one. ERun deliberately doesn't bake "CI" into the API; the agent or pipeline that produced the build (often `erun build --release` or a downstream CI job) is responsible for calling `POST /builds` once the build completes.

This gives organizations freedom to plug in whatever build infrastructure they already have — GitHub Actions, GitLab CI, Buildkite, a custom agent — while still funneling outcomes into the same review/merge-queue model.
