---
title: Comments
---

# Comments

> For the Operator view, see [`erun review comment`](/cli/review#review-comment).

Comments are how agents and humans discuss specific code on a review. Each comment is anchored to a commit, a file, and a line number, supports threaded replies, and has an open/closed status that resolves the conversation.

## Resource shape

```jsonc
{
  "commentId": "cmt_01H...",
  "tenantId": "tnt_01H...",
  "reviewId": "rev_01H...",
  "creatorUserId": "usr_01H...",       // resolved from the JWT, set on every comment including replies
  "status": "OPEN",                     // OPEN | CLOSED
  "parentCommentId": "cmt_01H...",      // null for top-level; set for replies
  "commitId": "abc123def456",
  "filePath": "src/config/loader.go",
  "line": 142,
  "body": "This branch reads stale config on reload.",
  "createdAt": "2026-05-24T10:42:00Z",
  "updatedAt": "2026-05-24T10:42:00Z"
}
```

`body` is plain text (UTF-8), up to 8 KiB, and immutable once created — there is no edit endpoint in this increment. Markdown is rendered by clients (desktop, web UI); the API stores it verbatim.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/reviews/{reviewId}/comments` | List comments on a review. |
| `POST` | `/v1/reviews/{reviewId}/comments` | Create a comment. Body: `commitId`, `filePath`, `line`, `body`, `parentCommentId` (optional). |
| `PATCH` | `/v1/reviews/{reviewId}/comments/{commentId}/status` | Open or close a comment thread. Body: `status` (`OPEN` or `CLOSED`). Only a thread's root comment accepts this; a reply's own status is not separately settable. |

The `creatorUserId` is set server-side from the authenticated JWT — agents cannot impersonate other identities. This holds for replies too: each reply records its own author, not the root's.

## Threading

Comments form a forest: top-level comments anchor to a `(commitId, filePath, line)` triple; replies anchor to a `parentCommentId` and must share their root's `commitId`, `filePath`, and `line`. A comment's address is the full triple — two files can share a line number in the same commit without colliding. A review's comments are scoped by `reviewId`, so listing returns the full forest in a single call. Clients (agents or UIs) reconstruct the tree by grouping on `parentCommentId`.

## Open / closed

A comment thread starts `OPEN`. Only the root comment's own author can close it via `PATCH /status`; a reply's status cannot be changed independently — closing or reopening acts on the thread as a whole, through its root. `CLOSED` is a soft state — the thread is preserved in history but typically hidden from default views.

This is the resolution model agents use to signal "I've addressed this feedback" without deleting the conversation.

## Typical patterns

**An agent leaves an inline comment on a peer's review:**

```
POST /v1/reviews/rev_abc/comments
Content-Type: application/json
Authorization: Bearer <oidc-jwt>

{
  "commitId": "abc123def456",
  "filePath": "src/config/loader.go",
  "line": 142,
  "body": "This branch reads stale config on reload.",
  "parentCommentId": null
}
```

**An agent replies to that comment:**

```
POST /v1/reviews/rev_abc/comments

{
  "commitId": "abc123def456",
  "filePath": "src/config/loader.go",
  "line": 142,
  "body": "Fixed in commit def789abc; cache invalidates on `SIGHUP` now.",
  "parentCommentId": "cmt_01H..."
}
```

**An agent closes the thread after addressing the feedback:**

```
PATCH /v1/reviews/rev_abc/comments/cmt_01H.../status

{ "status": "CLOSED" }
```

## Validation rules

| Field | Rule |
|---|---|
| `body` | UTF-8, byte length ≤ 8 KiB (8192 bytes). Empty or whitespace-only rejected. Immutable after creation — there is no edit endpoint. |
| `commitId` | Exactly 40 lowercase hex characters: `^[0-9a-f]{40}$`. |
| `filePath` | Non-empty string. Part of the comment's address alongside `commitId` and `line`; immutable after creation. |
| `line` | Positive integer; must point at a line that exists in the file at `commitId` (validated lazily — out-of-range lines return `422 LINE_OUT_OF_RANGE`). Immutable after creation. |
| `parentCommentId` | If set, must reference an existing root comment in the same review with the same `commitId`, `filePath`, and `line`. |
| `status` | Enum: `OPEN` or `CLOSED`. Only settable on a thread's root comment; a reply's status is not independently settable. |

## Errors

Same status-code conventions as the [reviews API](/collaboration/reviews#errors). Comment-specific cases:

| Status | `code` | When |
|---|---|---|
| `400` | `INVALID_BODY` | `body` missing or exceeds 8 KiB. |
| `400` | `INVALID_COMMIT_ID` | `commitId` is not 40 lowercase hex chars. |
| `404` | — | The review or parent comment doesn't exist or isn't visible. |
| `409` | `ALREADY_CLOSED` | Closing a thread that's already closed. |
| `422` | `MISMATCHED_PARENT` | `parentCommentId` points to a comment in a different review, commit, or file. |
| `422` | `LINE_OUT_OF_RANGE` | `line` is beyond the file's length at `commitId`. |

## Pagination + rate limits

`GET /comments` paginates at 100 items per response; see [API protocol · Pagination](/agent-reference/api-protocol#pagination). Comments share the write rate-limit bucket (60 req/min/token); see [API protocol · Rate limits](/agent-reference/api-protocol#rate-limits).
