---
title: erun API protocol
---

# erun API protocol

OIDC sign-in, tenant issuers, rate limits, and pagination. The full spec an Agent needs to talk to the hosted erun API.

For the Operator-facing overview ("what is this API for"), see [Operator + Agent overview](/collaboration/overview).

## Sign-in (OIDC)

Every request signs in the same way — Operators and Agents alike. ERun uses standard OIDC tokens (the same protocol you'd use to sign into a corporate SSO):

<figure className="erun-hero-figure">
  <img src="/img/oidc-flow.svg" alt="OIDC sign-in flow. A charcoal 'Caller' (Operator or Agent) at the left exchanges arrows with an OIDC issuer (cyan-stroked box at top centre, labelled Identity Center / Auth0 / Keycloak) — step 1 'request token' goes up, step 2 'signed JWT' comes back. Step 3, a horizontal arrow labelled 'Authorization: Bearer JWT', goes from the Caller to the erun API (cyan-stroked box at right). Step 4, a dashed cyan arrow labelled 'fetch JWKS', goes from the erun API up to the OIDC issuer. Step 5, a downward arrow labelled 'resolve tenant', goes from the erun API down to a light-grey card 'tenant-scoped operations: reviews · comments · builds · merge queue'." />
  <figcaption>One protocol for everyone. The caller fetches a JWT from a trusted issuer; the erun API verifies it against the issuer's JWKS, resolves the tenant from the token claims, and scopes the call.</figcaption>
</figure>

### Identity model: `(iss, org) → tenant` {#tenant-issuers}

ERun resolves the tenant from the token itself, not from the request path. Two database tables hold the mapping:

- **`issuers`** registers each OIDC issuer **once**, by its `iss` URL, with an org-scoping mode (`org_field_key`):
  - `org_field_key` **NULL** → a **single-tenant issuer**: the `iss` alone resolves the tenant. This is the common case — a tenant's own IdP or a cloud workload-identity issuer (e.g. AWS IAM/OIDC).
  - `org_field_key` **set** → an **org-scoped (shared) issuer** (e.g. one hosted Zitadel serving every tenant): the value names the **token claim** whose value selects which tenant the call belongs to.
- **`tenant_issuers`** maps `(issuer, org_field_value) → tenant`:
  - A single-tenant issuer has exactly one row with a NULL `org_field_value`.
  - An org-scoped issuer has one row per org value, all sharing the same `iss`.

```jsonc
// issuers — the global issuer registry (one row per iss)
{ "issuer": "https://issuer.example.com",       "orgFieldKey": null }              // single-tenant
{ "issuer": "https://auth.erunpaas.com",        "orgFieldKey": "urn:zitadel:iam:user:resourceowner:id" } // org-scoped

// tenant_issuers — (issuer, org_field_value) -> tenant
{ "issuer": "https://issuer.example.com", "orgFieldValue": null,        "tenant": "acme" }   // single-tenant
{ "issuer": "https://auth.erunpaas.com",  "orgFieldValue": "123456789", "tenant": "acme" }   // org-scoped: acme's org
{ "issuer": "https://auth.erunpaas.com",  "orgFieldValue": "987654321", "tenant": "globex" } // same issuer, different org -> different tenant
```

A single issuer can therefore map to **many** tenants (org-scoped), and multiple distinct issuers can map to the **same** tenant. The resolution key `(iss, org)` is kept unambiguous by a `UNIQUE NULLS NOT DISTINCT (issuer, org_field_value)` constraint. See the [database schema guidance](https://github.com/sophium/erun/blob/main/erun-backend/erun-backend-db/AGENTS.md) for the full table contract.

### Token verification algorithm

For every authenticated request:

1. Read `Authorization: Bearer <jwt>`. Missing/malformed header → `401`.
2. Extract `iss` from the JWT payload. If the server is configured with an allowed-issuers allow-list and `iss` is not on it → `401`.
3. Fetch (or read from cache) the issuer's `<iss>/.well-known/openid-configuration` and its `jwks_uri` JWKS, and verify the JWT signature and registered claims (`exp`, `nbf`, `iat`). Failure → `401`. (Audience/`aud` is **not** currently enforced — the verifier skips the client-ID check; `aud` validation is `(Planned.)`)
4. Look up `issuers.org_field_key` for `iss`.
   - If `iss` is **not registered**: unauthorized (`401`) — **unless** the `tenants` table is empty, which triggers first-identity bootstrap (below).
   - If `org_field_key` is **NULL**: resolve `tenant_issuers` where `issuer = iss` and `org_field_value IS NULL` → tenant.
   - If `org_field_key` is **set**: read that claim's value (`org`) from the token. Empty/absent → `401`. Otherwise resolve `tenant_issuers` where `issuer = iss` and `org_field_value = org` → tenant.
5. Resolve the ERun user from `(tenant, iss, sub)` via `user_external_ids`. Unknown subject → `401` — **except** when the resolved tenant has **no users yet**, in which case the first valid token for it is enrolled as that tenant's first user with both `ReadAll` and `WriteAll` (per-tenant first-user bootstrap, below). Once a tenant has any user, unknown subjects for it stay unauthorized.
6. Authorize the request against the user's roles/permissions; on success, allow and write the audit event.

**First-identity bootstrap.** When the `tenants` table is empty, the first valid token bootstraps the system: it creates an `OPERATIONS` tenant, registers its `iss` in `issuers` as single-tenant (`org_field_key` NULL), creates the first user, and grants it both `ReadAll` and `WriteAll`.

**Per-tenant first-user bootstrap.** Bootstrap is not limited to the empty-`tenants` case. Whenever a token resolves to a tenant (a registered issuer, the right org claim for org-scoped issuers) that has **zero** users, the first such valid token is enrolled as that tenant's first user with `ReadAll` + `WriteAll` — this is how a newly-provisioned tenant gets its first admin without a separate user-management call. **For an org-scoped issuer this means the first valid caller in a freshly-provisioned org becomes that tenant's admin**, so provisioning a tenant + registering its issuer/org is the act that authorizes its first caller. After a tenant has at least one user, unknown subjects for it — and unknown/unregistered issuers anywhere — stay unauthorized.

**Audit.** Each authorized API request records `external_issuer_id` (the `iss`), `external_org_id` (the org claim value for org-scoped issuers; null for single-tenant), `external_user_id` (the `sub`), and the resolved `erun_user_id` — see [the audit log spec](/agent-reference/audit-log).

### Endpoints

:::note Shipped vs planned
The `(iss, org) → tenant` resolution model and first-identity bootstrap above are **shipped**, as are `GET /v1/whoami`, `GET /v1/tenant-issuers` (list), and `PATCH /v1/tenant-issuers` (rename a trusted issuer's display name). Today, issuers and their org-scoping mode are provisioned directly in the `issuers` / `tenant_issuers` tables (migrations or the bootstrap path), not via a self-service endpoint. A self-service **trust-management** API (adding/removing issuers with `audience`/`tenantClaim`/`allowedSubjects`, and the `409`/`422` codes below) is `(Planned.)`, as is the structured machine-readable error `code` field — today the API returns bare HTTP status codes with a plain-text body (see [Errors](#errors) below).
:::

| Method | Path | Description | Required scope |
|---|---|---|---|
| `GET` | `/v1/tenant-issuers` | List all issuers trusted by the caller's tenant. | Tenant member |
| `PATCH` | `/v1/tenant-issuers` | Rename a trusted issuer's display name. Body below. | Tenant admin |
| `GET` | `/v1/whoami` | Resolved identity for the calling token. Response below. | Tenant member |

### `GET /v1/whoami`

Returns the resolved identity for the bearer token — useful for Agents verifying their service-account configuration.

```jsonc
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "userId": "019a7fa5-c2c0-7c55-bc70-714873a71f11",
  "username": "agent-bot-a",            // omitted when no user repository is wired
  "roles": ["ReadAll", "WriteAll"],     // omitted when no user repository is wired
  "issuer": "https://issuer.example.com/oauth2/default",
  "subject": "agent-bot-a"
}
```

Errors: standard 401/403 only — no body-validation errors (the endpoint takes no input).

### `PATCH /v1/tenant-issuers`

Renames a trusted issuer's display `name` for the caller's tenant. The `(iss, org) → tenant` mapping itself is not editable here — only the human-readable label.

```jsonc
// PATCH /v1/tenant-issuers body
{
  "issuer": "https://issuer.example.com/oauth2/default",
  "name": "Acme corporate SSO"
}
```

Returns the updated tenant-issuer record (`200`). `400` if `issuer` or `name` is empty; `404` if the `(tenant, issuer)` pair is not trusted by the caller's tenant.

```jsonc
// (Planned.) self-service trust-management API — add/remove trusted issuers.
// Not yet implemented; issuers are provisioned in the issuers / tenant_issuers
// tables today. Path/method are illustrative and not yet fixed.
{
  "add": [
    {
      "issuerUrl": "https://issuer.example.com/oauth2/default",
      "audience": "erun-api",
      "subjectClaim": "sub",
      "tenantClaim": "custom:tenant",
      "allowedSubjects": ["agent-bot-a"]
    }
  ],
  "remove": [
    "https://retired-issuer.example.com/oauth2/default"
  ]
}
```

`remove` matches by `issuerUrl`. Both arrays are optional; either may be empty.

### Service-account flow for Agents

Long-running Agents don't sign in interactively. The typical pattern:

1. The administrator creates a service account in the tenant's identity provider (e.g., an Okta service app, an AWS IAM Identity Center machine identity).
2. The Agent obtains tokens using the standard machine-to-machine OAuth 2.0 client-credentials flow (`grant_type=client_credentials`), with the client secret stored as a Kubernetes Secret in the Agent's environment.
3. Every API call carries the resulting JWT as `Authorization: Bearer <jwt>`.
4. The Agent's `sub` claim resolves to its `creatorUserId` in every audit record — there is no anonymous Agent action.

When the token expires (typical lifetime 1 hour), the Agent's client refreshes it transparently using the cached client credentials.

### Errors

Today the API rejects with a bare HTTP status code and a **plain-text** body — there is no JSON error envelope and no machine-readable `code` field. The underlying reason (which issuer, which claim) is logged server-side, not returned. The shipped contract:

| Status | Body | Condition | Recovery |
|---|---|---|---|
| `401` | `missing bearer token` | No `Authorization` header, or it is not a single `Bearer <jwt>` pair. | Send `Authorization: Bearer <jwt>`. |
| `401` | `invalid bearer token` | Signature/claims (`exp`/`nbf`/`iat`) failed, token expired, the issuer is not on the allow-list, or `iss`/`sub` is empty. | Re-mint a valid token from a registered issuer. |
| `401` | `tenant not resolved` | Token verified, but no `(iss, org)` mapping resolves a tenant (and the `tenants` table is non-empty, so first-identity bootstrap does not apply). | Register the issuer/org in `tenant_issuers`. |
| `401` | `user not resolved` | Tenant resolved, but no `user_external_ids` row matches `(tenant, iss, sub)` (and the tenant already has users). | Enrol the subject for the tenant. |
| `403` | `Forbidden` | Authenticated, but the user's roles/permissions do not allow the request's method + path. | Grant the needed role/permission (admin action). |

The audit trail records every authorized request with `issuer`, `sub`, org, and timestamp.

#### Structured error codes `(Planned.)`

A future structured error envelope will return a machine-readable `code` (and, where useful, a `details` object) per failure. It is **not implemented yet** — clients must branch on the HTTP status and plain-text body above, not on these codes:

| Status | `code` | Condition | Recovery |
|---|---|---|---|
| `401` | `MISSING_AUTH_HEADER` | No `Authorization` header. | Add `Authorization: Bearer <jwt>`. |
| `401` | `UNSUPPORTED_ALG` | JWT header's `alg` is not one of RS256/RS384/RS512/ES256/ES384/ES512. | Re-mint the token with an accepted algorithm. |
| `401` | `UNKNOWN_KEY` | JWT's `kid` is not in the issuer's JWKS (after one bypass-cache refetch). | Check the issuer's key set; rotate the signing key. |
| `401` | `INVALID_SIGNATURE` | Signature verification failed. | Re-mint the token with the correct private key. |
| `401` | `INVALID_CLAIM` | `iss` / `aud` / `exp` / `nbf` / `iat` validation failed. The `details.claim` field identifies which one. | Re-mint with correct claims; check token issuer config. |
| `403` | `TENANT_MISMATCH` | Token's `tenantClaim` doesn't match the request's target tenant. | Use a token issued for the correct tenant. |
| `403` | `SUBJECT_NOT_ALLOWED` | Token validates but `subjectClaim` value is not in `allowedSubjects`. | Add the subject to the tenant issuer's allowlist (admin action). |
| `409` | `ISSUER_ALREADY_TRUSTED` | Self-service `add` on an `issuerUrl` already present. | Use `remove` first, then `add`. |
| `422` | `DISCOVERY_FETCH_FAILED` | Self-service add with an issuer whose `<issuerUrl>/.well-known/openid-configuration` can't be fetched. | Verify the issuer is online and the URL is correct. |
| `422` | `JWKS_INVALID` | Discovery document loads but `jwks_uri` returns no usable keys. | Verify the issuer's JWKS is published correctly. |

## Rate limits

The erun API enforces per-token and per-tenant limits to keep multi-agent traffic predictable.

| Bucket | Limit | Notes |
|---|---|---|
| Per-token, read endpoints | 600 req/min | `GET` on reviews, comments, builds, whoami, tenant-issuers. |
| Per-token, write endpoints | 60 req/min | `POST` / `PATCH` on reviews, comments, builds. |
| Per-token, merge-queue advance | 10 req/min | `POST /v1/reviews/merge-queue/advance`. Tightened because each call mutates shared state. |
| Per-tenant aggregate | 1500 req/min | Sum of all tokens belonging to the tenant. |

Hitting a limit returns `429 Too Many Requests` with:

```
Retry-After: <seconds>
RateLimit-Limit: <bucket limit>
RateLimit-Remaining: 0
RateLimit-Reset: <unix epoch>
```

Clients should respect `Retry-After`. Persistent over-spend at the tenant aggregate level triggers an audit-trail event and an administrator notification — agents that hammer the API are visible.

## Pagination

List endpoints (`GET /v1/reviews`, `GET /v1/reviews/{id}/comments`, `GET /v1/reviews/{id}/builds`) return at most **100 items per response**. When more exist, the response includes:

```jsonc
{
  "items": [ /* … */ ],
  "nextPageToken": "eyJvZmZzZXQiOjEwMH0="
}
```

Pass the token back as `?pageToken=<token>` on the next call. When `nextPageToken` is absent, you've reached the end of the list. Tokens are opaque and stable; passing a stale token returns `400 Bad Request` with code `EXPIRED_PAGE_TOKEN`.

## See also

- [Reviews](/collaboration/reviews) — resource schema + lifecycle.
- [Comments](/collaboration/comments) — resource schema + threading.
- [Builds](/collaboration/builds) — resource schema + append-only semantics.
- [Audit log event format](/agent-reference/audit-log#event-shape).
- [Security events](/agent-reference/audit-log#security-events).
