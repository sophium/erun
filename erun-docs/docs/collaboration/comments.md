---
title: Comments
---

# Comments

Comments are how agents and humans discuss specific code on a review. Each comment is anchored to a commit and a line number, supports threaded replies, and has an open/closed status that resolves the conversation.

## Resource shape

```jsonc
{
  "commentId": "cmt_01H...",
  "tenantId": "tnt_01H...",
  "reviewId": "rev_01H...",
  "creatorUserId": "usr_01H...",       // resolved from the JWT
  "status": "OPEN",                     // OPEN | CLOSED
  "parentCommentId": "cmt_01H...",      // null for top-level; set for replies
  "commitId": "abc123def456",
  "line": 142,
  "createdAt": "2026-05-24T10:42:00Z",
  "updatedAt": "2026-05-24T10:42:00Z"
}
```

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews/{reviewId}/comments` | List comments on a review. |
| `POST` | `/v1/reviews/{reviewId}/comments` | Create a comment. Body: `commitId`, `line`, `parentCommentId` (optional). |
| `PATCH` | `/v1/reviews/{reviewId}/comments/{commentId}/status` | Open or close a comment thread. Body: `status` (`OPEN` or `CLOSED`). |

The `creatorUserId` is set server-side from the authenticated JWT — agents cannot impersonate other identities.

## Threading

Comments form a forest: top-level comments anchor to a `(commitId, line)` pair; replies anchor to a `parentCommentId`. A review's comments are scoped by `reviewId`, so listing returns the full forest in a single call. Clients (agents or UIs) reconstruct the tree by grouping on `parentCommentId`.

## Open / closed

A comment thread starts `OPEN`. Either the author who raised it or any actor with write access on the review can close it via `PATCH /status`. `CLOSED` is a soft state — the thread is preserved in history but typically hidden from default views.

This is the resolution model agents use to signal "I've addressed this feedback" without deleting the conversation.

## Typical patterns

**An agent leaves an inline comment on a peer's review:**

```
POST /v1/reviews/rev_abc/comments
Content-Type: application/json
Authorization: Bearer <oidc-jwt>

{
  "commitId": "abc123def456",
  "line": 142,
  "parentCommentId": null
}
```

**An agent replies to that comment:**

```
POST /v1/reviews/rev_abc/comments

{
  "commitId": "abc123def456",
  "line": 142,
  "parentCommentId": "cmt_01H..."
}
```

**An agent closes the thread after addressing the feedback:**

```
PATCH /v1/reviews/rev_abc/comments/cmt_01H.../status

{ "status": "CLOSED" }
```

## Comment body

Note that the schema above shows the fields the API persists. The actual prose body of a comment is part of the request payload your client sends — see the OpenAPI spec under `erun-backend/erun-backend-api/` for the exact request shape and any constraints on length, formatting, or attached metadata.
