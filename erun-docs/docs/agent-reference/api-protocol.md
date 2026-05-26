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

### Tenant issuers

Each tenant declares one or more **trusted issuers**. A token is accepted only if it was minted by one of them.

```jsonc
// TenantIssuer
{
  "issuerUrl": "https://issuer.example.com/oauth2/default",  // unique within the tenant
  "audience": "erun-api",                                     // expected `aud` claim
  "subjectClaim": "sub",                                       // claim used as creator user id (default: "sub")
  "tenantClaim": "custom:tenant",                              // optional; when set, ERun matches this claim against the tenant name
  "allowedSubjects": ["agent-bot-a", "agent-bot-b"],           // optional allow-list of `sub` values
  "createdAt": "2026-05-24T10:00:00Z"
}
```

### Token verification algorithm

For every authenticated request:

1. Read `Authorization: Bearer <jwt>`. Missing header → `401`.
2. Decode the JWT header to extract `alg` and `kid`. Unsupported `alg` (anything outside `RS256`, `RS384`, `RS512`, `ES256`, `ES384`, `ES512`) → `401` with code `UNSUPPORTED_ALG`.
3. Resolve the caller's tenant from the request path (`/v1/<resource>` is tenant-bound; the tenant is identified by the `tenantClaim` value validated below). Load the tenant's `TenantIssuer[]` set.
4. For each issuer: fetch (or read from cache) `<issuerUrl>/.well-known/openid-configuration`. Cache TTL **60 minutes**; on cache miss or expiry, re-fetch synchronously.
5. From the discovery document, fetch `jwks_uri` → JWKS. Cache TTL **15 minutes**.
6. Find the key matching `kid` in the JWKS. If absent: re-fetch the JWKS once (bypassing cache). If still absent → `401` with code `UNKNOWN_KEY`.
7. Verify the JWT signature against the key. Failure → `401` with code `INVALID_SIGNATURE`.
8. Validate registered claims: `iss` matches the issuer; `aud` matches `TenantIssuer.audience`; `exp` > now; `nbf` ≤ now (or absent); `iat` is reasonable (< 24h in the past). Any miss → `401` with code `INVALID_CLAIM`.
9. If `TenantIssuer.tenantClaim` is set: read that claim's value from the token; it must equal the request's target tenant name. Mismatch → `403` with code `TENANT_MISMATCH`.
10. If `TenantIssuer.allowedSubjects` is set: the token's `subjectClaim` value (default `sub`) must be in the list. Miss → `403` with code `SUBJECT_NOT_ALLOWED`.
11. Resolve the token's `subjectClaim` value to a stable `creator_user_id`; emit `signin.success` to the audit log.
12. Allow the request.

Any path that returns `401` / `403` also emits a `signin.failure` audit event with the reason code.

### Endpoints

| Method | Path | Description | Required scope |
|---|---|---|---|
| `GET` | `/v1/tenant-issuers` | List all issuers trusted by the caller's tenant. | Tenant member |
| `PATCH` | `/v1/tenant-issuers` | Add or remove trusted issuers. Body shape below. | Tenant admin |
| `GET` | `/v1/whoami` | Resolved identity for the calling token. Response below. | Tenant member |

### `GET /v1/whoami`

Returns the resolved identity for the bearer token — useful for Agents verifying their service-account configuration.

```jsonc
{
  "creatorUserId": "agent-bot-a",
  "tenant": "myapp",
  "actorKind": "agent",                  // "operator" | "agent"
  "issuer": "https://issuer.example.com/oauth2/default",
  "subject": "agent-bot-a",
  "audience": "erun-api",
  "expiresAt": "2026-05-25T15:31:02Z"
}
```

Errors: standard 401/403 only — no body-validation errors (the endpoint takes no input).

```jsonc
// PATCH /v1/tenant-issuers body
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

| Status | `code` | Condition | Recovery |
|---|---|---|---|
| `401` | `MISSING_AUTH_HEADER` | No `Authorization` header. | Add `Authorization: Bearer <jwt>`. |
| `401` | `UNSUPPORTED_ALG` | JWT header's `alg` is not one of RS256/RS384/RS512/ES256/ES384/ES512. | Re-mint the token with an accepted algorithm. |
| `401` | `UNKNOWN_KEY` | JWT's `kid` is not in the issuer's JWKS (after one bypass-cache refetch). | Check the issuer's key set; rotate the signing key. |
| `401` | `INVALID_SIGNATURE` | Signature verification failed. | Re-mint the token with the correct private key. |
| `401` | `INVALID_CLAIM` | `iss` / `aud` / `exp` / `nbf` / `iat` validation failed. The `details.claim` field identifies which one. | Re-mint with correct claims; check token issuer config. |
| `403` | `TENANT_MISMATCH` | Token's `tenantClaim` doesn't match the request's target tenant. | Use a token issued for the correct tenant. |
| `403` | `SUBJECT_NOT_ALLOWED` | Token validates but `subjectClaim` value is not in `allowedSubjects`. | Add the subject to the tenant issuer's allowlist (admin action). |
| `409` | `ISSUER_ALREADY_TRUSTED` | `PATCH /v1/tenant-issuers add` on an `issuerUrl` already present. | Use `remove` first, then `add`. |
| `422` | `DISCOVERY_FETCH_FAILED` | `PATCH` with an issuer whose `<issuerUrl>/.well-known/openid-configuration` can't be fetched. | Verify the issuer is online and the URL is correct. |
| `422` | `JWKS_INVALID` | Discovery document loads but `jwks_uri` returns no usable keys. | Verify the issuer's JWKS is published correctly. |

The audit trail records every successful and failed sign-in attempt with `issuer`, `sub`, IP, and timestamp.

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
