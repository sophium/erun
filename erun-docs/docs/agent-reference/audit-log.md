---
title: Audit log format
---

# Audit log format

The exact event shape, retention windows, and security-event catalogue. For the Operator-facing view ("what the audit trail is for, and how it's used"), see [Operator in the loop · The audit trail](/collaboration/operator-in-the-loop#the-audit-trail).

## Three layers

| Layer | What it captures | Retention |
|---|---|---|
| In-environment trace | Every `erun` invocation; per-action `docker` / `helm` / `git` commands; `--dry-run` plans. | Pod lifetime (`/var/log/erun/audit.log`). Idle-stop wipes. |
| Per-environment MCP events | Every `tools/call` with `argv` / `cwd` / exit code; `idle.activity` snapshots; `doctor` outcomes. | 30 days inside the pod. |
| Hosted erun API events | Every review, comment, status transition, recorded build, security event. | Durable. Lifetime of the tenant. |

## Event shape

All three layers emit a common JSON-lines event:

```jsonc
{
  "timestamp": "2026-05-25T14:31:02Z",
  "tenant": "myapp",
  "environment": "feature-a",                // omitted for cross-env API events
  "actor": {
    "id": "usr_01H...",                       // or "agent-bot-a" for service accounts
    "kind": "operator",                        // "operator" | "agent"
    "source": "cli"                            // "cli" | "mcp" | "api"
  },
  "action": "erun.build",                     // verb in dotted form
  "target": { "kind": "image", "name": "myapp-api", "version": "1.0.77" },
  "result": "ok",                              // "ok" | "dry_run" | "error"
  "details": {
    "argv": ["docker","build","-t","myapp-api:1.0.77",".",
             "--build-arg","TARGETOS=linux"]
  }
}
```

A `--dry-run` invocation records the same event with `result: "dry_run"` and the same `details` it would produce on a real run — that's what makes dry-run a true preview.

### Field semantics

| Field | Constraints |
|---|---|
| `timestamp` | RFC3339 UTC. |
| `tenant` | Tenant name, always set. |
| `environment` | Set for in-pod and MCP events; omitted for cross-env API events. |
| `actor.id` | The resolved `creator_user_id` from the OIDC `sub` claim. Service-account IDs follow `agent-<name>` or `bot-<name>` conventions. |
| `actor.kind` | Lowercase enum: `operator` or `agent`. |
| `actor.source` | Lowercase enum: `cli`, `mcp`, or `api`. |
| `action` | Dotted verb — see [Action verb catalogue](#action-verb-catalogue). |
| `target` | Object with `kind` (required) + per-kind fields — see [Target shapes](#target-shapes). |
| `result` | Lowercase enum: `ok`, `dry_run`, or `error`. Errors include `details.error` with a one-line message. |
| `details` | Free-form action-specific payload. |

### Action verb catalogue

Verbs are dotted, lowercase, and stable. The complete enumeration:

| Source | Verb pattern | Specific values |
|---|---|---|
| CLI | `erun.<command>` | `erun.init`, `erun.open`, `erun.add`, `erun.list`, `erun.build`, `erun.push`, `erun.deploy`, `erun.doctor`, `erun.mcp`, `erun.release`, `erun.delete`, `erun.version` |
| MCP | `mcp.<tool>` | `mcp.idle`, `mcp.doctor`, `mcp.list`, `mcp.version`, `mcp.logs`, `mcp.build`, `mcp.push`, `mcp.deploy`, `mcp.release`, `mcp.open`, `mcp.init`, `mcp.delete`, `mcp.scaffold`, `mcp.regenerate-chart`, `mcp.migrate-deps`, `mcp.extract-component`, `mcp.add-ingress`, `mcp.raw` |
| API | `api.<resource>.<verb>` | `api.reviews.create`, `api.reviews.list`, `api.reviews.get`, `api.reviews.status.update`, `api.comments.create`, `api.comments.list`, `api.comments.status.update`, `api.builds.create`, `api.builds.list`, `api.builds.get`, `api.merge-queue.list`, `api.merge-queue.advance`, `api.whoami`, `api.tenant-issuers.list`, `api.tenant-issuers.update`, `api.audit-events.list` |

New verbs require an entry in this enumeration; clients can rely on the closed set.

### Target shapes

The `target` object's per-kind fields:

| `target.kind` | Required fields | Notes |
|---|---|---|
| `image` | `name`, `version` | The OCI image. `name` is the un-prefixed component name (e.g. `myapp-api`); the registry is implicit from env config. |
| `chart` | `name`, `revision` | Helm chart name + the release revision after the action. |
| `review` | `id`, `name`, `targetBranch` | Review resource. |
| `comment` | `id`, `reviewId` | Comment resource. |
| `build` | `id`, `reviewId`, `commitId` | Build resource. |
| `tenant-issuer` | `issuerUrl` | Trusted-issuer entry. |
| `environment` | `name` | Env name; the action's `tenant` field carries the tenant. |
| `merge-queue` | `targetBranch` | Per-target-branch queue. |
| `audit-events` | (none beyond `kind`) | Used for the meta-action of listing audit events. |

Unknown kinds are reserved; clients should treat them as opaque (forward-compatible).

## Security events

Alongside the action log, the hosted API maintains a separate `audit-events` table for security-relevant events. These are durable, queryable, and tenant-admin-visible. The minimum captured set:

| Event | When | Why it matters |
|---|---|---|
| `signin.success` | OIDC sign-in passes verification. | Establishes who connected from where. |
| `signin.failure` | OIDC token fails verification (signature, audience, expiry, subject not allowed). | Probes and misconfiguration land here. |
| `tenant-issuer.add` | Admin adds a trusted OIDC issuer via `PATCH /v1/tenant-issuers`. | New trust roots are security-critical. |
| `tenant-issuer.remove` | Admin removes a trusted issuer. | Detects accidental removal of the only active issuer. |
| `review.status.transition` | `PATCH /v1/reviews/{id}/status` changes the status. | The merge path is auditable end-to-end. |
| `mergequeue.advance` | `POST /v1/reviews/merge-queue/advance` promotes a head. | Promotions are the moment a branch reaches main. |
| `release.tag.push` | A release-tagged image is published to the registry. | The artefact-promotion boundary. |
| `release.tag.delete` | A previously-published release tag is removed (`erun build --release --force` or registry-side). | Tag rewrites mutate "immutable" history; visible at the audit boundary. |
| `env.delete` | An environment is removed. | Namespace tear-down captured. |
| `ratelimit.exceeded` | Caller hits a per-token or per-tenant limit. | Surfaces agents that hammer the API. |

Each event carries: `eventId`, `timestamp`, `tenant`, `actor` (same shape as the action log), `event` (string), `details` (event-specific). The schema is append-only — events are never edited or deleted.

### Query API

`GET /v1/audit-events` — admin-only. Returns durable audit events for the caller's tenant.

| Query parameter | Type | Default | Effect |
|---|---|---|---|
| `from` | RFC3339 | — | Inclusive lower bound. Required. |
| `to` | RFC3339 | now | Exclusive upper bound. |
| `event` | string | — | Filter by `event` value (`signin.failure`, `mergequeue.advance`, …). Repeatable. |
| `actor` | string | — | Filter by `actor.id`. |
| `pageToken` | opaque | — | Continuation from a prior response. |

Response:

```jsonc
{
  "items": [
    {
      "eventId": "evt_01H...",
      "timestamp": "2026-05-25T14:31:02Z",
      "tenant": "myapp",
      "actor": { "id": "agent-bot-a", "kind": "agent", "source": "api" },
      "event": "signin.success",
      "details": { "issuer": "https://issuer.example.com/oauth2/default", "ip": "203.0.113.42" }
    }
  ],
  "nextPageToken": "eyJvZmZzZXQiOjEwMH0="
}
```

Max **100 items per page**; pagination follows the [common rules](/agent-reference/api-protocol#pagination). Rate-limit bucket: per-token read endpoints.

| Status | `code` | When |
|---|---|---|
| `400` | `MISSING_FROM` | `from` parameter absent. |
| `400` | `INVALID_TIMESTAMP` | `from` or `to` is not RFC3339. |
| `400` | `EXPIRED_PAGE_TOKEN` | `pageToken` is stale or malformed. |
| `403` | `NOT_TENANT_ADMIN` | Caller is a tenant member but not an admin. |

### Layer-level format differences

| Layer | Format |
|---|---|
| In-pod trace (`/var/log/erun/audit.log`) | JSON-lines, one event per line, no envelope. |
| MCP per-env events (in-pod, 30-day retention) | JSON-lines, identical shape to the in-pod trace; same file rotated by date. |
| Hosted API response | JSON object `{ items: [...], nextPageToken }` wrapping the same event shape. |

A consumer reading both surfaces deserialises each line of JSON-lines, or each element of `items`, with the same schema.

## Retention guarantees

Plan for security-sensitive history accordingly: anything you need to reconstruct after a pod restart belongs on the API side. The in-pod trace is for live debugging; the durable record lives in the hosted API.

## See also

- [erun API protocol](/agent-reference/api-protocol) — rate limits, pagination, OIDC sign-in.
- [Operator in the loop](/collaboration/operator-in-the-loop) — the Operator-facing view of audit.
