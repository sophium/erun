---
title: Audit log format
---

# Audit log format

The audit event shape and the hosted API's read contract. For the Operator-facing view ("what the audit trail is for, and how it's used"), see [Operator in the loop · The audit trail](/collaboration/operator-in-the-loop#the-audit-trail).

## What's captured today

Every successfully authorized hosted-API request writes one row to the tenant's audit trail: authentication middleware logs it after token verification, tenant resolution, user resolution, and endpoint authorization all succeed. A request rejected before that point — missing or invalid token, unknown issuer, unknown user, denied permission — is never logged, so the trail records what happened, not every attempt.

The schema also carries columns for CLI and MCP activity (`type: "CLI"` / `type: "MCP"`, `cliCommand`, `mcpTool`). `type: "MCP"` is now written: an MCP tool call authenticates with the same bearer token as any other caller, so it sends an extra header (`X-Erun-Mcp-Tool`, naming the tool) that the audit middleware reads to classify the row and populate `mcpTool` instead of `apiMethod`/`apiPath`. Only MCP tools that call through to this API get classified this way — `review_*`, `platform_*`, `gate_*`/`exec_gate-run_*`/`exec_reconcile-bypass`, `expose`/`unexpose`'s platform DNS path, and `build`'s self-report of its own outcome. Most MCP tools (`exec_raw`, `list`, `idle`, job-status polling, and the rest of the purely in-pod surface) never call this API at all and so never write a row here, by design — this table is the hosted API's own request log, not a general activity log for everything an Agent does in a pod. `type: "CLI"` is still `(Planned.)`; every row from a CLI-driven call reads `type: "API"` today.

## Event shape

```jsonc
{
  "auditEventId": "01973f9e-2f10-7c31-8f2a-1234567890ab", // UUIDv7
  "tenantId": "01973f00-0000-7000-8000-000000000001",
  "erunUserId": "01973f01-0000-7000-8000-000000000002", // the resolved ERun user, not the raw external subject
  "externalUserId": "auth0|abc123",                      // the OIDC subject the identity provider presented
  "externalIssuerId": "https://issuer.example.com/",
  "externalOrgId": "org_123",                            // org/resource-owner claim, org-scoped issuers only; omitted otherwise
  "type": "API",                                          // "API" | "MCP" | "CLI"
  "apiMethod": "GET",                                     // set when type is "API"
  "apiPath": "/v1/reviews/{review_id}",                   // the canonical route template, never a concrete URL
  "createdAt": "2026-05-25T14:31:02Z"
}
```

A `CLI` row would carry `cliCommand` instead of `apiMethod`/`apiPath`. **(Planned.)** An `MCP` row carries `mcpTool` (e.g. `"review_show"`) instead of `apiMethod`/`apiPath`, for the MCP tools named above.

### Field semantics

| Field | Constraints |
|---|---|
| `auditEventId` | UUIDv7, unique per event. |
| `tenantId` | The tenant the event belongs to. |
| `erunUserId` | The resolved ERun user id — not the external identity provider's subject. |
| `externalUserId` | The external subject the identity provider presented. |
| `externalIssuerId` | The OIDC `iss` claim that resolved the tenant. |
| `externalOrgId` | The org/resource-owner claim for an org-scoped (shared) issuer; omitted for a single-tenant issuer. |
| `type` | `API`, `MCP`, or `CLI`. |
| `apiMethod` | HTTP method; set when `type` is `API`. |
| `apiPath` | The canonical route template registered for the endpoint (e.g. `/v1/reviews/{review_id}`), matching the same template `role_permissions` uses for authorization — never a concrete URL with resolved IDs or a query string. |
| `mcpTool` | The MCP tool name (e.g. `"review_show"`); set when `type` is `MCP`. The request still hit the same route as an equivalent API call — `apiMethod`/`apiPath` are omitted from the row only because `type` is `MCP` instead, not because the request took a different route. |
| `createdAt` | RFC3339 UTC. |

### What the read API never returns

The write path also has `cliParameters`, `mcpToolParameters`, and `apiParameters` columns for serialized call arguments, but `GET /v1/audit-events` (below) never selects any of them and the response has no field for them, regardless of what a write path stores there. An MCP tool such as `cloud_inject_aws_credentials` takes credentials as call arguments — the tool name (`mcpTool`) is exactly the kind of thing an audit trail should surface, but the argument payload is exactly the kind of thing it must not leak back out through a read endpoint. The same holds for `cliParameters` once CLI audit logging lands, and for `apiParameters` today: the one API caller that populates it, `POST /v1/reviews/merge-queue/override-advance`, writes the overriding operator's reason there specifically so it is durably captured, not so it becomes readable back through this endpoint.

## Query API

`GET /v1/audit-events` — authorized the same way every other list endpoint is: the caller's `ReadAll` permission plus PostgreSQL row-level security scope the response to the caller's own tenant. There is no separate "audit admin" permission — a company tenant's caller with `ReadAll` reads that tenant's own rows; an operations-tenant caller reads across every tenant's rows, the same cross-tenant reach `ReadAll` already grants that role for every other resource.

| Query parameter | Type | Effect |
|---|---|---|
| `since` | RFC3339 | Inclusive lower bound on `createdAt`. |
| `until` | RFC3339 | Inclusive upper bound on `createdAt`. |
| `erunUserId` | UUID | Filter to one resolved ERun user. |
| `type` | `API` \| `MCP` \| `CLI` | Filter by source. |
| `apiMethod` | string | Filter by HTTP method (meaningful with `type=API`). |
| `apiPath` | string | Filter by the canonical route template, not a concrete URL. |
| `cursor` | opaque | Continuation token from a prior response's `nextCursor`. |
| `limit` | integer | Page size; defaults to 50, capped at 200. |

Response:

```jsonc
{
  "events": [ /* event objects, newest first */ ],
  "nextCursor": "2026-05-25T14:31:02.000000000Z,01973f9e-2f10-7c31-8f2a-1234567890ab"
}
```

`nextCursor` encodes the `(createdAt, auditEventId)` of the last row in the page — a keyset, not an offset. `audit_events` is append-only and unbounded, so an offset would skip or repeat rows as new events are appended ahead of a page still being read; the keyset can't. `nextCursor` is omitted once a page reaches the end of the trail.

Filters map onto the table's indexes: no filter (or `since`/`until` alone) uses the tenant/time index, `erunUserId` uses the tenant/user/time index, and `apiMethod`/`apiPath` together use the tenant/api/time index — every filter this endpoint exposes has a matching index, rather than falling back to a sequential scan as the trail grows.

| Status | When |
|---|---|
| `400` | A query parameter fails to parse — `since`/`until` not RFC3339, `cursor` malformed, `limit` not an integer. |
| `403` | The caller's role has no read permission for `GET` on this path. |

## Security events

There is no separate, curated table of security-relevant events (sign-in failures, issuer changes, release-tag rewrites, and so on) distinct from the generic trail above. **(Planned.)** Today, a security-relevant action is auditable only in the same way as any other request: it is one more row in `audit_events`, filterable by `apiMethod`/`apiPath` like any other API call.

## See also

- [Operator in the loop](/collaboration/operator-in-the-loop) — the Operator-facing view of audit.
