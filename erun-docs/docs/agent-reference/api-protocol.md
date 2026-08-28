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
2. Extract `iss` from the JWT payload. The API trusts two kinds of issuer (issue #674), dispatched on scheme — the same multi-issuer model as the [MCP edge](#mcp-edge), so the API and every edge authenticate identically:
   - **`file://` desktop key** — when `iss` equals the configured trusted `file://<path>` desktop issuer (`ERUN_API_DESKTOP_PUBLIC_KEY_PATH`): verify the EdDSA signature against the injected public key (`alg` hard-locked to `EdDSA`, closing the alg-confusion class), and enforce `exp` and the `erun-api` audience. This is the desktop / e2e path — a desktop-signed token authenticates with **no live IdP**, exactly as for the MCP edge.
   - **`https://` OIDC issuer** — otherwise: if an allowed-issuers allow-list is configured and `iss` is not on it → `401`.
3. (OIDC path) Fetch (or read from cache) the issuer's `<iss>/.well-known/openid-configuration` and its `jwks_uri` JWKS, and verify the JWT signature and registered claims (`exp`, `nbf`, `iat`). Failure → `401`. (For OIDC tokens, audience/`aud` is **not** currently enforced — the verifier skips the client-ID check; `aud` validation for OIDC is `(Planned.)`. The `file://` desktop path **does** enforce the `erun-api` audience, so a token minted for an MCP env — audience `erun-mcp:<tenant>/<env>` — cannot be replayed against the API.)
4. Look up `issuers.org_field_key` for `iss`.
   - If `iss` is **not registered**: unauthorized (`401`) — **unless** the `tenants` table is empty, which triggers first-identity bootstrap (below).
   - If `org_field_key` is **NULL**: resolve `tenant_issuers` where `issuer = iss` and `org_field_value IS NULL` → tenant.
   - If `org_field_key` is **set**: read that claim's value (`org`) from the token. Empty/absent → `401`. Otherwise resolve `tenant_issuers` where `issuer = iss` and `org_field_value = org` → tenant.
5. Resolve the ERun user from `(tenant, iss, sub)` via `user_external_ids`. Unknown subject → `401` — **except** when the resolved tenant has **no users yet**, in which case the first valid token for it is enrolled as that tenant's first user with both `ReadAll` and `WriteAll` (per-tenant first-user bootstrap, below). Once a tenant has any user, unknown subjects for it stay unauthorized.
6. Authorize the request against the user's roles/permissions; on success, allow and write the audit event.

**First-identity bootstrap.** When the `tenants` table is empty, the first valid token bootstraps the system: it creates an `OPERATIONS` tenant, registers its `iss` in `issuers` as single-tenant (`org_field_key` NULL), creates the first user, and grants it both `ReadAll` and `WriteAll`.

**Per-tenant first-user bootstrap.** Bootstrap is not limited to the empty-`tenants` case. Whenever a token resolves to a tenant (a registered issuer, the right org claim for org-scoped issuers) that has **zero** users, the first such valid token is enrolled as that tenant's first user with `ReadAll` + `WriteAll` — this is how a newly-provisioned tenant gets its first admin without a separate user-management call. **For an org-scoped issuer this means the first valid caller in a freshly-provisioned org becomes that tenant's admin**, so provisioning a tenant + registering its issuer/org is the act that authorizes its first caller. After a tenant has at least one user, unknown subjects for it — and unknown/unregistered issuers anywhere — stay unauthorized.

**Audit.** Each authorized API request records `external_issuer_id` (the `iss`), `external_org_id` (the org claim value for org-scoped issuers; null for single-tenant), `external_user_id` (the `sub`), and the resolved `erun_user_id` — see [the audit log spec](/agent-reference/audit-log).

### Per-env MCP edge authentication {#mcp-edge}

The per-environment `erun-mcp` server is exposed publicly (Traefik routes it at `mcp.<tenant>-<env>.services.<base-domain>`) and its `raw` tool can `kubectl exec`, so it is RCE-sensitive and **must always be authenticated** once a trust anchor is configured. The edge resolves the tenant from the verified token's issuer — the same `(iss) → tenant` model as the REST API, applied per URL.

The runtime chart configures each edge with a set of trusted issuers mapping each issuer to the tenant it authenticates (`ERUN_MCP_TRUSTED_ISSUERS`, a JSON `{"<issuer>":"<tenant>"}` object; `ERUN_MCP_TRUSTED_ISSUER` + `ERUN_TENANT` is single-issuer sugar). A request is authorized when:

1. `Authorization: Bearer <jwt>` is present — missing → `401`.
2. The token's `iss` is a trusted issuer for this edge — untrusted → `401`; the mapped value is the resolved tenant.
3. The signature verifies against that issuer's key, and `exp` and the audience (`aud`) match — the per-env audience (`erun-mcp:<tenant>/<environment>`) means a token minted for one environment cannot be replayed against another, or against the REST API (whose `file://` path enforces its own `erun-api` audience — issue #674). The REST API's OIDC path does not yet enforce `aud` (see the [verification algorithm](#token-verification-algorithm) above).
4. The resolved tenant matches **this** environment's tenant (a per-env edge serves exactly one tenant) — a token resolving to another tenant → `401`. Tenant-scoped tools are likewise pinned to the edge's own environment: a `tenant`/`environment` argument that differs from the pod's identity is refused, so a caller can never drive one env's MCP to act on another (issue #657).

An edge can trust **multiple issuers at once**, of two kinds, dispatched by the *configured* issuer's scheme (not the token's claimed `iss`, so the verification path can't be attacker-chosen):

- **`https://` OIDC issuer** — the chart's `mcpAuth.issuer`/`mcpAuth.enabled` values support pointing the edge at an OIDC issuer's JWKS instead of a local key, verified through the same shared verifier the REST API uses. Nothing in erun currently writes this: `erun deploy` never resolves an OIDC issuer for an env, so the only way a running edge ends up on this path is a hand-set chart value. Every deploy-driven env — desktop or hosted — uses the `file://` key path below.
- **`file://` key** (issues #655, #686) — a self-contained trust anchor instead of an OIDC IdP: an Ed25519 public key injected into the runtime pod when the env's runtime is deployed (`erun deploy --mcp-auth-public-key <key>`, or `erun init --mcp-auth-public-key <key>`, which folds it into init's create-time deploy so the desktop needs no separate post-init redeploy). The signer stamps a `file://<path>` `iss` naming that public key; the edge loads the key from that path and verifies the EdDSA signature, with `alg` hard-locked to `EdDSA` (closing the alg-confusion class). Two signers use it, and each env picks exactly one:
  - **Desktop** — the desktop generates the key (`desktopid.key`) once and signs each token locally.
  - **Hosted (console)** — the **backend** is the signer, the hosted twin of the desktop: it holds the MCP signing key (`ERUN_API_MCP_SIGNING_KEY_PATH`) and mints a per-env token on the console's behalf via [`POST /v1/environments/{id}/mcp-token`](#mcp-token-endpoint). The env's server-side deploy Job injects the backend's own public key automatically (no Operator action, no `--mcp-auth-public-key`) — so the console never holds a signing key and every hosted env's edge is authenticated by default once the backend has a signing key configured (issue #1084).

The `file://` anchor is **sticky across redeploys**: the deploy that injects the key records its path on the env ([`mcpauthpublickeypath`](/reference/configuration#envconfig)) as it injects it — not after the rollout, so a rollout that fails leaves the anchor named rather than nameless — and every later deploy of the runtime chart rethreads it, so a plain version bump cannot leave the edge unauthenticated. Turning the anchor off takes the explicit `erun deploy --no-mcp-auth`, and a deploy that would drop authentication the live release still has is refused with the trusted key named — see [`erun deploy` · MCP-auth stickiness](/agent-reference/cli-flags#deploy-mcp-auth).

When no trust anchor is configured the edge stays loopback-only (legacy, unauthenticated) — a desktop or hosted deploy always configures one. Capability/scope-gated authorization of *individual* tools (e.g. restricting the RCE-capable `raw` to admin-scoped tokens, while a read-only token sees only the read tools) is `(Planned.)` — it rides on the hosted role source (issue #606).

### Endpoints

:::note Shipped vs planned
The `(iss, org) → tenant` resolution model and first-identity bootstrap above are **shipped**, as are `GET /v1/whoami`, `GET /v1/tenant-issuers` (list), and `PATCH /v1/tenant-issuers` (rename a trusted issuer's display name). New tenants and their issuer mapping can be registered through the operations-only `POST /v1/tenants` below; for an existing tenant, additional issuers and their org-scoping mode are still provisioned directly in the `issuers` / `tenant_issuers` tables (migrations or the bootstrap path), not via a tenant-self-service endpoint. `POST /v1/users` enrolls additional users beyond the first-user bootstrap. A tenant-self-service **trust-management** API (a tenant adding/removing its own issuers with `audience`/`tenantClaim`/`allowedSubjects`, and the `409`/`422` codes below) is `(Planned.)`, as is the structured machine-readable error `code` field — today the API returns bare HTTP status codes with a plain-text body (see [Errors](#errors) below).
:::

| Method | Path | Description | Required scope |
|---|---|---|---|
| `GET` | `/v1/platform` | Unauthenticated self-discovery a caller resolves **before** signing in: this instance's own `issuer`, `apiUrl`, `consoleUrl`, OIDC client ids, and white-label surface (`brand`, `docsUrl`, `tagline`, `logoUrl`). Response below. | None — no bearer required |
| `GET` | `/v1/tenant-issuers` | List all issuers trusted by the caller's tenant. | Tenant member |
| `PATCH` | `/v1/tenant-issuers` | Rename a trusted issuer's display name. Body below. | Tenant admin |
| `GET` | `/v1/whoami` | Resolved identity for the calling token. Response below. | Tenant member |
| `GET` | `/v1/config` | The console's read model over the per-tenant erun config: `{tenant, environments[], contexts[]}`. | Tenant member |
| `GET` | `/v1/environments` | List the tenant's environments. | Tenant member |
| `POST` | `/v1/environments` | Register an environment in the caller's tenant, bound to a referenced context; when the deploy executor is configured, a runtime env with a pinned version also starts its durable server-side deploy (`202`). Body below. | Tenant member (write) |
| `GET` | `/v1/environments/{environment_id}` | Fetch one environment by id. | Tenant member |
| `POST` | `/v1/environments/{environment_id}/deploy` | Deploy an already-registered runtime env at a published version — the retry and version-change path (`202`). Body below. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/stop` | Scale a runtime env's Deployment to zero — the server-side equivalent of `erun stop`. Does not change the env's provisioning `status`. Body-less. | Tenant member (write) |
| `DELETE` | `/v1/environments/{environment_id}` | Start tearing down a runtime env's namespace (skipped if it never deployed) and removing its row — the server-side equivalent of `erun delete`. Asynchronous: `202 Accepted` with the row at `status: deleting`; poll to see it converge. Not recoverable. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/mcp-token` | Mint a per-env MCP bearer token (`{token, audience}`) for the caller to present to the env's `erun-mcp` edge. Body-less. Response below. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/dns01-token` | Mint a per-env DNS-01 broker token (`{token, audience}`), the credential the cluster's cert-manager DNS-01 webhook presents to the [DNS-01 broker](#dns01-broker). Body-less. Response below. | Tenant member (write) |
| `GET` | `/v1/contexts` | List the tenant's cloud contexts (managed clusters). | Tenant member |
| `POST` | `/v1/contexts` | Register a cloud context (managed cluster) and, when provisioning is configured, start its durable live bootstrap (`202`). Body below. | Tenant member (write) |
| `GET` | `/v1/contexts/{context_id}` | Fetch one cloud context by id, including its provisioning `status`. | Tenant member |
| `PUT` | `/v1/cloud-provider-aliases/{alias}` | Register/update the tenant's BYO-cloud credentials (encrypted at rest), resolved when provisioning a context. Body below. | Tenant member (write) |
| `POST` | `/v1/provision` | Return the complete, ordered **plan** to provision a hosted env (quota check → placement → context bootstrap → namespace → env registration → runtime deploy → auth-edge wiring → exposure) for the caller's tenant. Preview-only; no execution, no writes. Body below. | Tenant member (write) |
| `POST` | `/v1/tenants` | Register a new tenant plus its OIDC issuer mapping. Operations-only. Body below. | Operations only |
| `GET` | `/v1/tenants` | List every tenant (operations-only caller), or a single-item list containing just the caller's own tenant otherwise. | Tenant member (read) |
| `POST` | `/v1/users` | Enroll a user in the caller's tenant, or — operations-only — an explicitly named other tenant. Body below. | Tenant member (write); cross-tenant needs Operations |
| `GET` | `/v1/users` | List the caller's tenant's users, or — operations-only — an explicitly named other tenant's via `?tenantId=`. | Tenant member (read); cross-tenant needs Operations |
| `GET` | `/v1/roles` | List the caller's tenant's roles with their permissions. | Tenant member (read) |
| `POST` | `/v1/roles` | Create a tenant-owned role with one or more permissions. Body below. | Tenant member (write) |
| `GET` | `/v1/users/{user_id}/roles` | List a user's assigned roles. | Tenant member (read) |
| `POST` | `/v1/users/{user_id}/roles` | Grant a role to a user. Body below. | Tenant member (write) |
| `DELETE` | `/v1/users/{user_id}/roles/{role_id}` | Revoke a role from a user; refused if it would leave the tenant with no user able to grant roles. | Tenant member (write) |
| `POST` | `/v1/invites` | Create a revocable, single-use invite for the caller's tenant, or — operations-only — an explicitly named other tenant. Body below. | Tenant member (write); cross-tenant needs Operations |
| `GET` | `/v1/invites` | List the caller's tenant's outstanding (unconsumed) invites, or — operations-only — an explicitly named other tenant's via `?tenantId=`. | Tenant member (read); cross-tenant needs Operations |
| `DELETE` | `/v1/invites/{invite_id}` | Revoke an outstanding invite. `204` on success. | Tenant member (write) |
| `POST` | `/v1/invites/accept` | Consume an invite token and enroll the invitee. **Unauthenticated** — the invite token in the body is the credential. Body below. | None (public) |

`GET /v1/config` is the console's read model over the per-tenant erun config — the backend DB is the system of record for the tenant's environments and cloud contexts, and this endpoint returns them denormalized as the on-disk erun config shape. All of these reads are tenant-scoped by row-level security, so a token only ever sees its own tenant's rows.

### `GET /v1/platform` {#platform-endpoint}

The only endpoint besides `/healthz` that requires **no credential of any kind** — registered outside the auth middleware, directly on the mux next to it. A caller (chiefly the console SPA, but also the `erun cloud` provider below) has to resolve this instance's own issuer, API/console URLs, OIDC client ids, and display brand *before* it can sign in, so the endpoint carries no bearer token and no tenant scoping. (`POST /v1/invites/accept` below, the [DNS-01 broker](#dns01-broker), and the [registry token service](#registry-token-endpoint) are also registered outside the OIDC auth middleware, but each authenticates its own distinct credential — an invite token, a per-env M2M token, and HTTP Basic respectively — rather than requiring none at all.) No instance's name is ever hardcoded in a client; this is how one discovers it. It exists so **one built console image can serve any erunpaas instance** (issue #603): issuer, brand, and OIDC client ids are runtime config the API answers with, never baked into the frontend build.

```jsonc
// 200 response — every field optional; unset ones are empty strings, never omitted
{
  "issuer": "https://auth.acme.erunpaas.com",
  "apiUrl": "https://api.acme.erunpaas.com",
  "consoleUrl": "https://console.acme.erunpaas.com",
  "consoleClientId": "console-app-id",
  "cliClientId": "cli-app-id",
  "brand": "Acme",
  "docsUrl": "https://docs.acme.erunpaas.com",
  "tagline": "Ship it, prove it.",
  "logoUrl": "https://acme.erunpaas.com/logo.svg"
}
```

Every field is optional and independently sourced. `issuer`/`apiUrl`/`consoleUrl`/`brand`/`docsUrl`/`tagline`/`logoUrl` come from the env's [`platform:` block](/reference/configuration#platform-block) (threaded in at deploy via `--set-string platform.*`); an unset value renders as an empty string, **never** an error or a missing field. `consoleClientId`/`cliClientId` come from the `erun-zitadel` chart's OIDC application bootstrap (see [below](#zitadel-oidc-bootstrap)) via an optional ConfigMap — absent when that chart hasn't run, or on a platform with no hosted IdP, again rendering as `""` rather than failing the response.

`docsUrl` defaults to `https://docs.<basedomain>` when the platform block sets a base domain, so an instance links its own documentation with nothing configured. `tagline` and `logoUrl` have no default — empty is what keeps the client's bundled product text and generic mark in place. `logoUrl` is deliberately an **absolute URL**, not a path this API serves: one built console image serves every instance and carries no brand asset, so the logo lives wherever the operator hosts it.

**How the console uses it.** On load, before rendering the sign-in prompt, the console fetches this endpoint and drives its OIDC Authorization Code + PKCE flow from `issuer` + `consoleClientId` (see [Sign-in](#sign-in-oidc) for the flow itself; `src/auth/auth.ts` is the implementation). A console built against an **older API with no `/v1/platform`** gets a `404`, and against a **newer API with the fields left unset** gets `200` with empty strings — both fall back to its own build-time `VITE_OIDC_ISSUER`/`VITE_OIDC_CLIENT_ID` (a local-dev override only), rather than failing to render. `brand`, `docsUrl`, `tagline`, and `logoUrl` are what the signed-out landing page renders — the document title, the docs link, the `<h1>` pitch, and the header mark respectively — each falling back to a bundled product default when empty, so a half-configured instance renders a coherent page rather than a blank hero. A `logoUrl` the browser cannot load falls back to the same generic mark as an unset one, so a moved or blocked asset never leaves a broken image on the front door. `apiUrl`/`consoleUrl`/`cliClientId` are carried for other clients (a CLI `erun login` flow); the console does not consume them yet.

**How `erun cloud` uses it.** A caller (an `erun cloud init <platform-api-url>`-style flow) uses this response to then fetch `<issuer>/.well-known/openid-configuration` and proceed with the Device Authorization Grant (falling back to Authorization Code + PKCE when the issuer advertises no device endpoint) against `cliClientId`. See the [erun cloud provider](#erun-cloud-provider) section below.

**Error behaviour.** No input to validate and no authentication performed, so a server that implements this endpoint always returns `200`. A `404` instead means an older API predates the endpoint entirely — the recovery is the client-side fallback described above, not an operator action.

#### Zitadel OIDC application bootstrap {#zitadel-oidc-bootstrap}

The `erun-zitadel` chart provisions the two OIDC applications `consoleClientId`/`cliClientId` above resolve to, idempotently, via a sidecar in the same pod as Zitadel core (it needs the shared bootstrap volume to read the org-owner PAT core writes):

- **`erun-console`** — a `OIDC_APP_TYPE_USER_AGENT` (SPA) app, Authorization Code + PKCE, redirect/post-logout URI derived from the env's `platform.consoleUrl`.
- **`erun-cli`** — a `OIDC_APP_TYPE_NATIVE` (public) app supporting both the Device Authorization Grant (`OIDC_GRANT_TYPE_DEVICE_CODE`) and Authorization Code + PKCE with loopback redirect URIs (`http://127.0.0.1/callback`, `http://localhost/callback`).

Both are configured with `accessTokenType: OIDC_TOKEN_TYPE_JWT` — load-bearing, since erun's bearer verifier validates a JWT via OIDC discovery + JWKS and rejects Zitadel's default opaque access token with `401 invalid bearer token`. The sidecar publishes the resulting client ids to a `<tenant>-zitadel-oidc-clients` ConfigMap in the release namespace, which the `erun-backend-api` chart reads via an optional `configMapKeyRef` (`optional: true` — absent ConfigMap, absent env var, empty string in the `/v1/platform` response above).

#### erun cloud provider {#erun-cloud-provider}

`erun-common` models a hosted erun platform as a fourth cloud provider type (`CloudProviderERun = "erun"`, alongside `aws` and `cloudflare`) — this is how an operator or Agent authenticates to a hosted erun platform with the `erun` CLI/MCP transports (landing in a later stage on top of this API surface):

- **Init** (`InitERunCloudProvider`) calls `GET /v1/platform` then the issuer's `.well-known/openid-configuration`, and records the alias, API URL, issuer, and `cliClientId` — no sign-in yet.
- **Login** tries the Device Authorization Grant first (the only flow that works with no browser, e.g. from inside a pod): it surfaces `user_code` and `verification_uri_complete` and polls the token endpoint honoring `authorization_pending`/`slow_down`/`expired_token`. When the issuer's discovery advertises no device endpoint, it falls back to Authorization Code + PKCE on a loopback listener.
- The refresh token is persisted through the same `CloudSecretStore`/`TokenRef` pattern the Cloudflare provider uses — the secret never lands in `erun-config.yaml` — and the access token is cached with its expiry, refreshed silently via the `refresh_token` grant when it expires.

### `GET /v1/whoami`

Returns the resolved identity for the bearer token — useful for Agents verifying their service-account configuration.

```jsonc
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "userId": "019a7fa5-c2c0-7c55-bc70-714873a71f11",
  "username": "agent-bot-a",            // omitted when no user repository is wired
  "roles": ["ReadAll", "WriteAll"],     // omitted when no user repository is wired
  "capabilities": [                     // see "The capability set" below
    { "method": "GET", "path": "/v1/reviews" },
    { "method": "GET", "path": "/v1/reviews/{review_id}" }
  ],
  "issuer": "https://issuer.example.com/oauth2/default",
  "subject": "agent-bot-a"
}
```

Errors: standard 401/403 only — no body-validation errors (the endpoint takes no input).

### The capability set {#capability-set}

`capabilities` is the caller's **effective permission set**: every registered route this caller would be let through to, already expanded from the exact and pattern rules their roles carry. It exists so a client can render only what the caller can actually do — and say why when it cannot — instead of showing an empty list or a button that fails with a `403` after the click.

Its contract:

| Property | Rule |
|---|---|
| Shape | An array of `{ "method", "path" }`. `path` is the **canonical route template** (`/v1/reviews/{review_id}`), the same form `role_permissions.api_path` and the audit trail use — never a concrete request URL. |
| Membership | A pair is present if and only if authorization would permit it. It is computed by the same query and the same matcher the authorization middleware runs per request, so the set and enforcement cannot drift. Pattern rules (`api_method_pattern` / `api_path_pattern`) resolve identically here, anchors included. |
| Candidates | Only routes this API instance actually serves. A route that is not registered (because its executor is unconfigured, say) is not in the set. |
| Empty vs absent | `[]` means the caller may do nothing — an answer. `null`, or the field missing entirely, means this platform did not report a capability set at all; a client must then attempt the call and report the server's own refusal, rather than hide a surface the caller may in fact be able to use. |
| Authority | Advisory, for rendering only. The server re-evaluates every request; a stale set never grants anything. |
| Never derive from `roles` | A role's name says nothing about what a tenant granted it, so a surface gated on the name is wrong for every custom role and wrong again the moment a role's permissions change. `capabilities` is the only input. |

Errors: when the capability set cannot be resolved, `GET /v1/whoami` fails with `500` rather than answering without one — an omitted set would otherwise read as "you may do nothing" and hide surfaces the caller can use. `GET /v1/whoami` is itself authorized like every other route, so a caller holding no permissions at all is refused it with `403` and never reaches an empty set; a client has to treat that refusal as "you have no access to this tenant", not as a transport fault.

For how a client is expected to degrade from this set — a list the caller may not read is not an empty list, an action they may not perform is not an enabled button — see the permission-degradation rules in `erun-ui/AGENTS.md` and `erun-console/AGENTS.md`.

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

### `POST /v1/environments`

Registers an **environment** in the caller's tenant. The tenant is resolved from the token — never from the body — so a token can only register an environment under its own tenant (row-level security scopes the write). The environment **runs in a referenced context**: `contextId` points at one of the tenant's cloud contexts, and the composite `(tenant_id, context_id)` foreign key enforces that the context belongs to the same tenant.

```jsonc
// POST /v1/environments body
{
  "name": "prod",              // required — a DNS-1123 label (lowercase letters, digits, internal hyphens)
  "type": "runtime",           // required — one of "runtime", "remote-agent", "local-agent"
  "contextId": "019a7fa5-…",   // optional — see "Placement" below
  "kubernetesContext": "primary", // optional, remote-agent/local-agent only — see "Placement" below
  "runtimeVersion": "1.2.3",   // optional — pinned runtime chart version
  "preview": false             // optional — resolve and return the POST /v1/provision plan instead of creating the row
}
```

**Placement.** For a `runtime` environment, `contextId` picks the cluster the deploy/stop/delete Job authenticates against: name one of the tenant's own registered [cloud contexts](/concepts/hosted-platform#single-cluster-placement) to place there (validated to belong to the caller's tenant and to have room), or leave it unset to auto-select one of the tenant's own registered, running contexts with room — falling back to the platform's own cluster when the tenant has registered none. Once a tenant has registered at least one context, exhausting all of them (none running, or all at capacity) is a `409` rather than a silent fallback. A raw `kubernetesContext` string names no known credential and is refused with `400` for a `runtime` environment; `remote-agent`/`local-agent` environments — never server-side deployed — may set either field freely with no placement decision made.

**`preview`.** When `true`, the endpoint validates the body (including the placement resolution above) and returns the identical `{plan, quotaOk}` shape [`POST /v1/provision`](#post-v1provision) returns, **without creating the row** — the executing path previewing itself. `preview: true` short-circuits before the quota check would otherwise `409`; like `POST /v1/provision`, an at-cap tenant still gets the full plan back, with `quotaOk: false`.

On success the endpoint persists the row and returns it. The status code tells the caller whether a deploy started:

- **`201 Created`** — the row is registered config only, no deploy. This is the response when the deploy executor is not configured, or the env is not a `runtime` env, or no `runtimeVersion` is pinned (nothing to deploy). `status` is `registered`.
- **`202 Accepted`** — the deploy executor is configured **and** this is a `runtime` env with a pinned `runtimeVersion`, so the backend has started the durable server-side deploy. The row comes back at `status: registered` and moves to `provisioning` → `running`/`failed` as the deploy runs; poll `GET /v1/environments/{id}` (or watch the `GET /v1/config` read model) to follow it.

```jsonc
// 201 / 202 response (same body shape; 202 means a deploy is now running)
{
  "environmentId": "019a7fa5-c2c0-7c55-bc70-714873a71f30",
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "name": "prod",
  "type": "runtime",
  "kubernetesContext": "primary",
  "contextId": "019a7fa5-c2c0-7c55-bc70-714873a71f20",
  "runtimeVersion": "1.2.3",
  "status": "registered",       // lifecycle: registered → provisioning → running/failed; delete adds deleting → (gone) / deletion-blocked
  "deployedVersion": "1.2.2",   // omitted until a deploy has landed; see below
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

A newly-registered environment is `registered` — the row exists but nothing is deployed. The server-side deploy executor then moves it `provisioning` → `running`/`failed` and sets `provisionError` on failure. A requested teardown moves it to `deleting`, from which it either disappears entirely (the row is hard-deleted once the namespace is confirmed gone) or lands on `deletion-blocked` with `deleteError` naming why — see [`DELETE /v1/environments/{environment_id}`](#delete-endpoint). `running` never survives a delete attempt. `status`, `provisionError`, `exposeError`, and `deleteError` all appear identically on `GET /v1/environments/{id}` and in the `GET /v1/config` read model.

**`runtimeVersion` vs `deployedVersion`.** `runtimeVersion` is the version the environment is **pinned** to — operator-authored, set at registration. `deployedVersion` is the version a deploy **actually installed**, written when that deploy reaches `running`. They are equal in the steady state and diverge in exactly two cases: a [`POST /v1/environments/{id}/deploy`](#deploy-endpoint) that named a different version, and a failed deploy — which leaves `deployedVersion` on the version the cluster is still running, because a deploy that failed did not remove what was already there. `deployedVersion` is omitted until the environment's first successful deploy.

**Per-tenant environment-count quota.** After validating the body and before persisting, the endpoint enforces the tenant's environment-count cap: it compares how many environments the tenant already has against the cap and rejects the registration with HTTP `409` once the tenant is at or over it. The cap defaults to **10** and is overridden per tenant by a `tenant_quotas.max_environments` row. That override row is set by the operations-only [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) endpoint (below). Both the count and the cap are read under row-level security, so each is scoped to the caller's own tenant. **Environments mid-teardown do not count.** The comparison excludes rows at `deleting` and `deletion-blocked`: the delete that would free the slot is the same call that is stuck, so counting a wedged teardown would lock a tenant out of its own allowance. The aggregate resource budget below counts differently — it uses the tenant's runtime-environment count as-is, mid-teardown rows included.

**Per-environment resource-cap floor.** For a `runtime` environment, the endpoint also checks the tenant's `maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb` caps against the `erun-devops` chart's own minimum requirement — cpu `8000m`, memory `17832Mi`, storage `72Gi`, the pod's `erun-devops` and `erun-dind` containers summed together, since a Kubernetes `ResourceQuota` counts every container in the pod — and rejects with `409` if the tenant's cap is configured below it, naming the shortfall. This catches a knowable failure before it happens: a namespace `ResourceQuota` sized under the stock runtime pod's own footprint would otherwise let the create call succeed and only fail later, when Kubernetes refuses to admit the pod. [`POST /v1/environments/{id}/deploy`](#deploy-endpoint) re-checks the same floor, since an operator can lower a tenant's quota after the environment already exists. See [Quotas](/concepts/hosted-platform#quotas) for how the caps are enforced and derived.

**Aggregate resource budget (#1113).** For a `runtime` environment, the endpoint also projects the tenant's total CPU/memory/storage if this environment is admitted — `(existing runtime environment count + 1) × the per-environment cap` — against `maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb`, and rejects with `409` naming which resource and by how much the projection would exceed the budget. This is the separate tenant-wide ceiling the per-environment floor above does not cover: raising `maxEnvironments` alone lets a tenant multiply its total footprint with nothing capping the sum. [`POST /v1/environments/{id}/deploy`](#deploy-endpoint) re-checks it too, using the count as-is (a redeploy does not add a new environment). See [Quotas](/concepts/hosted-platform#quotas) for the full budget model.

**Published runtime image precondition.** Also for a `runtime` environment with a pinned `runtimeVersion` (or an explicit [`POST .../deploy`](#deploy-endpoint)), the endpoint best-effort checks that `<registry>/<tenant>-devops:<runtimeVersion>` — the exact image the deploy Job pulls — resolves in the registry, and rejects with `409` when it is **confirmed** absent (a `ghcr.io` `404` on an anonymous-pull-token manifest request). This is deliberately conservative: a private image, an unreachable registry, or any other inconclusive outcome never blocks the call, so the check only ever catches the one case it can prove — a tenant that has never published a runtime image at all — instead of gating every deploy on a registry probe succeeding. Registries other than `ghcr.io` are not checked and always pass.

**Server-side deploy executor.** When configured, the backend deploys the runtime chart itself: it runs the deploy as a Kubernetes `Job` in the tenant's `<tenant>-devops` runtime image (which carries `erun` + `helm` + `kubectl`) under a curated `<tenant>-env-provisioner` ClusterRole ServiceAccount (see [Provisioner RBAC](/concepts/hosted-platform#provisioner-rbac) — not `cluster-admin`), invoking `erun deploy <tenant> <env> --version <runtimeVersion>` (plus `--max-cpu`/`--max-memory`/`--max-storage` when the tenant's quota resolves — see [Quotas](/concepts/hosted-platform#quotas)) and, when the platform is configured for it, chaining `erun expose <tenant> <env> mcp --ip <ip> --skip-if-unconfigured` (see [Automatic exposure](/concepts/hosted-platform#automatic-exposure)) — and watches the Job to completion — succeeded → `running`, failed → `failed` with the reason on `provisionError` (a failed expose fails the whole Job, so the environment is never recorded `running` while unreachable). `stop`/`delete` run the same way with `erun stop`/`erun delete -y` (see [`POST .../stop`](#stop-endpoint) and [`DELETE`](#delete-endpoint) below). A durable workflow (DBOS) wraps deploy, keyed by environment id, so a control-plane restart resumes an in-flight deploy rather than double-deploying. `stop` still runs synchronously within the request (its Job is a short `kubectl scale`), but `delete` no longer does: a durable workflow wraps it too, keyed by the delete **attempt**, because a namespace stuck on an unsatisfiable finalizer can wedge for as long as Kubernetes is willing to sit in `Terminating` — see [`DELETE`](#delete-endpoint). The deploy image is `<registry>/<tenant>-devops:<runtimeVersion>`.

**Bootstrapping the Job's own environment.** The Job's `command` replaces the image's entrypoint, so none of the entrypoint's usual setup runs — no kubeconfig, and no `~/.config/erun/<tenant>/<env>/config.yaml` for `erun deploy` to resolve (a freshly-registered environment was never baked into any image). The Job's command therefore seeds both explicitly before running `erun deploy`: a minimal `type: runtime` config for the tenant and environment, and a kubeconfig context — built from the pod's own mounted ServiceAccount token when the environment placed into the platform's own cluster, or (see [Placement](/concepts/hosted-platform#single-cluster-placement)) `kubectl config` commands authenticating against the placed context's own admin token when it named one. This keeps `erun deploy` itself an unchanged pure primitive — the Job is the caller supplying the environment's shape explicitly, the primitive still only ever consumes on-disk config exactly as it always has.

**What `provisionError` carries on a failed deploy.** The failure happens inside the Job, so the executor reads it back before the Job's TTL reaps the pod and records it verbatim under a `deploy job failed for version <version>:` prefix. Three sources, in order — the first that yields anything wins:

1. **The deploy's own output** — the tail of the Job pod's log (up to 40 non-empty lines, 4000 characters, keeping the *end*, since `erun deploy` prints its failure last). This is the actionable case: a version whose runtime chart search cannot confirm any coordinate surfaces the deploy's `RUNTIME_CHART_NOT_CONFIRMED` error in full, naming the version and every `<registry>/charts/<chart>` coordinate the [runtime chart search](/agent-reference/cli-flags#deploy-runtime-chart-search) probed and whether each was confirmed absent or unreadable, plus the three ways out that page specs; a coordinate the search *did* confirm but whose `helm pull` still failed surfaces as `MISSING_CHART_IN_REGISTRY` instead. This is the whole point of reading the Job's output back: the erun platform chart is published only beside the runtime image ERun releases, so an environment whose deploy registry holds nothing but its own application images has no chart there at any version — a failure the version alone never explains.
2. **The pod's status** — for a deploy that never logged a line, e.g. `deploy pod <name>: container deploy is waiting: ImagePullBackOff: <message>` when the cluster cannot pull `<registry>/<tenant>-devops:<version>`.
3. **The Job's terminal condition** — `deploy job <name> failed: DeadlineExceeded: …` when the pod is already gone (the Job's `activeDeadlineSeconds` is 30 minutes).

When none of the three yields anything, `provisionError` says so and names the `kubectl -n <platform-namespace> logs job/<job-name>` an Operator can run while a deploy is in flight. Reading the reason needs `pods` + `pods/log` `get`/`list`/`watch` in the platform namespace, which the `api.envDeployer.enabled` chart value already grants the API's ServiceAccount.

The executor is **opt-in and off by default** — it requires the backend to run in-cluster with a durable-workflow database, a kube client, and the deployer ServiceAccount configured (chart value `api.envDeployer.enabled`, which also provisions the SA and its curated `<tenant>-env-provisioner` ClusterRoleBinding). When it is off, or the env is not a `runtime` env, or no `runtimeVersion` is pinned, the endpoint is register-only (`201`) exactly as before.

Registration deploys an environment **once**. To deploy it again — retrying a failure, or moving it to another published version — use [`POST /v1/environments/{id}/deploy`](#deploy-endpoint).

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name` is not a DNS-1123 label (the env forms the `<tenant>-<env>` namespace), `type` is not one of `runtime`/`remote-agent`/`local-agent`, the body is not valid JSON, a `runtime` environment set a raw `kubernetesContext` (no known credential — see [Placement](/concepts/hosted-platform#single-cluster-placement) above), or a `runtime` environment's `contextId` does not resolve for the caller's tenant. | Send a DNS-1123 `name` and a valid `type`; reference a `contextId` you registered, or leave both unset for the platform's own cluster. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/environments`. | Send a valid token whose roles permit the write. |
| `409` | The tenant is at its environment-count cap (default `10` unless a `tenant_quotas` row overrides it); the body is `environment quota reached: this tenant already has <count> of <cap> environments`. `<count>` excludes environments at `deleting`/`deletion-blocked`. Not raised when `preview` is `true`. | Delete an unused environment (the slot frees as soon as the delete is accepted, not once the namespace finishes tearing down), or raise the tenant's cap via [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) (operations-only). |
| `409` | The tenant's resource caps are below the runtime pod's minimum (see "Per-environment resource-cap floor" above); the body names which cap (`maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb`), the required floor, and the shortfall. | Raise the named cap via `PUT /v1/tenants/{tenant_id}/quota`. |
| `409` | Admitting this environment would exceed the tenant's aggregate resource budget (see "Aggregate resource budget (#1113)" above); the body names which resource (`maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb`) and the projected total. | Raise the named budget, lower the per-environment cap, or delete an unused environment via `PUT /v1/tenants/{tenant_id}/quota`. |
| `409` | Placement (see [Placement](/concepts/hosted-platform#single-cluster-placement)) has no room: an explicit `contextId` is already at its `maxEnvironments`, or — once the tenant has registered at least one context — every registered context is full or not yet `running`. | Raise the named context's `maxEnvironments`, register another context, or delete an unused environment on that context. |
| `409` | The tenant's `<tenant>-devops:<runtimeVersion>` runtime image is confirmed absent from `ghcr.io` (see "Published runtime image precondition" above); the body is `runtime image … is not published: …`. Only raised once the row is already created and the deploy would otherwise start — the row stays `registered`, nothing to unwind. | Publish the image (`erun push` at that version) and retry via [`POST .../deploy`](#deploy-endpoint). |
| `500` | Persistence failed for a `remote-agent`/`local-agent` environment — e.g. `contextId` references a context that is not the caller's (the composite `(tenant_id, context_id)` foreign key is violated; a `runtime` environment's `contextId` is already validated synchronously into a `400` above) — or the request-scoped security context is missing (an internal wiring error). The row is persisted **before** the deploy is started, so a `failed to start provisioning` `500` (the deploy executor could not enqueue the durable workflow) leaves the environment registered — re-create is a no-op conflict; poll `GET /v1/environments/{id}` to confirm the row exists. | Reference a context owned by the caller's tenant; if it persists with a valid context, it is a server bug. |

### `POST /v1/environments/{environment_id}/deploy` {#deploy-endpoint}

Deploys an **already-registered** runtime environment. [`POST /v1/environments`](#post-v1environments) deploys an environment once, as a side effect of registering it; this endpoint is how an environment is deployed *again* — to retry a deploy that failed, or to move the environment to a different published runtime version. The environment is resolved from `{environment_id}` under row-level security, so a token can only deploy its own tenant's environments.

It composes the **pure `deploy` primitive**: the version must already be published, and the endpoint never builds or pushes. Nothing here mints a version.

```jsonc
// POST /v1/environments/{environment_id}/deploy body — optional in full
{
  "version": "1.3.0"   // optional — defaults to the environment's pinned runtimeVersion
}
```

A body-less request (no body at all, or `{}`) deploys the environment's own `runtimeVersion`. On success the endpoint returns **`202 Accepted`** with the environment row as it was read, having flipped it to `provisioning` and started the durable workflow. Poll `GET /v1/environments/{id}` (or the `GET /v1/config` read model) to follow it to `running`/`failed`; on `running`, `deployedVersion` names the version that landed.

**One deploy at a time.** Before starting the workflow the endpoint takes an atomic **claim** on the environment — a conditional write that sets `provisioning` only when the environment is not already deploying. A second request while one is in flight gets `409` rather than launching a second rollout into the same Helm release, where the loser's terminal status write could clobber the winner's. A claim left behind by a control plane that crashed mid-deploy goes **stale after 45 minutes** (longer than the deploy Job's own 30-minute deadline, so a claim is only stale once the run behind it cannot still be live) and becomes re-claimable, so an environment is never locked out permanently.

**Every deploy is a real re-run.** The deploy Job and its durable workflow are keyed by *attempt*, not by environment: each request mints an attempt id that names the Job (`erun-deploy-<tenant>-<env>-<version>-<attempt>`) and the workflow. Keying by environment would make both terminal after the first deploy, so a retry would silently replay the old outcome instead of running. The attempt id is part of the checkpointed workflow input, so a control-plane restart still resumes by re-watching the Job that attempt already created rather than starting a second one.

**Resource quota, re-checked.** Before claiming, the endpoint re-runs the same two checks [`POST /v1/environments`](#post-v1environments) does at create — the tenant's `maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb` caps against the runtime pod's minimum, and the aggregate `maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb` budget projection (using the environment's own existing runtime count, not +1, since a redeploy adds nothing new) — and rejects with `409` if either is now insufficient (see [Quotas](/concepts/hosted-platform#quotas)). This catches an operator lowering the tenant's quota (`PUT /v1/tenants/{tenant_id}/quota`) after the environment was already created: without the re-check, the next deploy would only discover the shortfall as a five-minute rollout timeout.

**Error behaviour.** Bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | The environment is not a `runtime` env (`only a runtime environment can be deployed`); no version resolves — the body omitted one and the environment has no pinned `runtimeVersion` (`version is required: …`); or the body is present but not valid JSON (`invalid request body`). | Deploy a runtime env, and name a published `version` when the environment has no pin. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers this write. | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (row-level security returns not-found for another tenant's env, never leaking its existence). | Deploy an environment id the caller's tenant owns. |
| `409` | The tenant's resource caps are now below the runtime pod's minimum, or admitting this redeploy would exceed the tenant's aggregate resource budget (see "Resource quota, re-checked" above); the body names which cap/budget and the shortfall. Checked before the claim, so nothing is left in `provisioning`. | Raise the named cap/budget via `PUT /v1/tenants/{tenant_id}/quota`. |
| `409` | A deploy is already in flight for this environment (`a deploy is already in progress for this environment`); the claim is held. | Poll `GET /v1/environments/{id}` until it leaves `provisioning`, or wait out the 45-minute stale window if the holder crashed. |
| `409` | The tenant's `<tenant>-devops:<version>` runtime image is confirmed absent from `ghcr.io` (see [`POST /v1/environments`](#post-v1environments)'s "Published runtime image precondition"). Unlike the create path, the claim already moved the row to `provisioning`, so this endpoint also marks it `failed` with the reason before responding — otherwise the environment would be stranded in `provisioning` with no workflow left to ever move it out. | Publish the image and retry. |
| `501` | The deploy executor is not configured (`the deploy executor is not configured`) — the backend has no durable-workflow database, kube client, or deployer ServiceAccount. | Enable `api.envDeployer.enabled` on the backend chart. |
| `500` | The claim write failed, or the durable workflow could not be enqueued (`failed to start deploy`). The claim is taken **before** the workflow starts, so this leaves the environment at `provisioning` with nothing running; it becomes re-deployable once the stale window elapses. | Retry after the stale window; if it persists, it is a server bug. |

### `POST /v1/environments/{environment_id}/stop` {#stop-endpoint}

Scales a `runtime` environment's Deployment to zero — the server-side equivalent of `erun stop`. Body-less. Runs a short-lived `erun stop <tenant> <env>` Job synchronously within the request (no durable workflow, unlike deploy — the underlying `kubectl scale` is seconds, not minutes) and returns the environment row as it was read; `status` is **not** changed — a stopped environment stays `running`, paused rather than torn down, so a later deploy or open wakes it without re-provisioning.

| Status | Condition | Recovery |
|---|---|---|
| `400` | The environment is not a `runtime` env. | Stop a runtime environment. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant. | Stop an environment id the caller's tenant owns. |
| `501` | The deploy/lifecycle executor is not configured. | Enable `api.envDeployer.enabled` on the backend chart. |
| `502` | The stop Job failed or could not be created; the response body carries the Job's own outcome. | Retry; if it persists, inspect the Job's pod log while it is still live. |

### `DELETE /v1/environments/{environment_id}` {#delete-endpoint}

Starts tearing down a `runtime` environment's namespace and removing its row — the server-side equivalent of `erun delete`. An environment that never successfully deployed (no `runtimeVersion`/`deployedVersion` to resolve a teardown image from) skips the Job entirely and the row is removed directly; a `remote-agent`/`local-agent` environment is never server-side deployed, so it is always a plain row removal. **Not recoverable.**

Body-less. On success the endpoint returns **`202 Accepted`** with the environment row as it was read, flipped to `status: deleting`, and the durable delete workflow started behind it — the same 202-then-poll shape [`POST /v1/environments`](#post-v1environments) and [`POST .../deploy`](#deploy-endpoint) already use. It does **not** wait for the teardown: a namespace stuck on an unsatisfiable finalizer can sit in `Terminating` for as long as Kubernetes is willing to, and a request that blocked on that would simply time out with the caller no wiser.

Poll `GET /v1/environments/{id}` (or the `GET /v1/config` read model) to follow it. It converges two ways:

- **Gone** — the row is hard-deleted once the namespace is confirmed torn down, so the poll starts returning `404`.
- **`deletion-blocked`** — the teardown did not complete, and `deleteError` names why: the stuck namespace's own conditions, verbatim. A `running` environment never stays `running` through a delete attempt; a namespace merely being asked to tear down is not up and serving any more.

**One delete at a time.** Before starting the workflow the endpoint takes an atomic **claim** on the environment — the same mechanism [`POST .../deploy`](#deploy-endpoint) uses — and a second request while one is in flight gets `409` rather than launching a second delete Job at the same namespace. A claim left behind by a control plane that crashed mid-delete goes **stale after 45 minutes** (longer than the delete Job's own 30-minute deadline) and becomes re-claimable. A row already at `deletion-blocked` is **always** re-claimable regardless of the stale window: that status means the previous attempt already reached a terminal outcome, so there is nothing in flight to race with — re-issuing `DELETE` on a blocked environment retries it immediately.

**Every delete is a real re-run.** The workflow is keyed by the delete *attempt*, not by the environment, for the same reason deploy is: an environment-keyed id would replay a completed `deletion-blocked` attempt's cached result instead of actually running again.

**Automatic retry.** A background reconciler re-attempts every environment at `deleting` or `deletion-blocked` every 5 minutes, taking over the claim the same way an operator's retry would. A namespace that finishes terminating on its own therefore converges to gone within minutes, with no operator action and no re-issued request.

**Cleanup that outlives the row.** A successful teardown chains a best-effort `erun unexpose` to remove the per-env DNS record — see [Env teardown](/agent-reference/networking-spec#unexposing). Its failure is logged on the control plane, not recorded on the environment, since the row is removed in the same workflow step.

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant. | Delete an environment id the caller's tenant owns. |
| `409` | A delete is already in flight for this environment (`a delete is already in progress for this environment`); the claim is held. Not raised for an environment already at `deletion-blocked`, which is always re-claimable. | Poll `GET /v1/environments/{id}` until it leaves `deleting`, or wait out the 45-minute stale window if the holder crashed. The reconciler retries it either way. |
| `501` | The deploy/lifecycle executor is not configured. | Enable `api.envDeployer.enabled` on the backend chart. |
| `500` | The durable workflow could not be started (`failed to start delete`). The claim is taken **before** the workflow starts, so the endpoint marks the row `deletion-blocked` with the reason rather than stranding it at `deleting` with nothing running. | Retry; the reconciler also picks it up within its next cycle. |

### `POST /v1/environments/{environment_id}/mcp-token` {#mcp-token-endpoint}

Mints a per-env MCP bearer token the caller presents to that environment's `erun-mcp` edge (at `mcp.<tenant>-<env>.services.<base-domain>`). The **backend** signs the token — the hosted twin of the desktop signing locally — so a browser console needs no signing key. The environment is resolved from `{environment_id}` under row-level security, so a token can only mint for its own tenant's environments. The minted token's `sub` is the caller's ERun user, `aud` is the per-env `erun-mcp:<tenant>/<environment>`, and `iss` is the fixed in-pod `file://` path the deploy injects the backend's public key at, so the edge verifies it (see [Per-env MCP edge authentication](#mcp-edge)). It is short-lived (~1 hour); mint a fresh one when it lapses.

The endpoint takes **no body**. On success it returns HTTP `200`:

```jsonc
// 200 response
{
  "token": "<eddsa-jwt>",
  "audience": "erun-mcp:acme/prod"
}
```

**Backend signing key.** The signer is enabled by pointing `ERUN_API_MCP_SIGNING_KEY_PATH` at the backend's Ed25519 private key (PKCS#8 PEM) — on a hosted deploy, the `erun-backend-api` chart's `api.mcpSigning.secretName` value mounts that key Secret and sets the path (opt-in; unset leaves the endpoint at `501`). The matching public key is what a deploy injects into the env (`erun deploy --mcp-auth-public-key`), so the edge trusts backend-signed tokens.

**Usable once the env is deployed.** A minted token only authenticates against a **deployed** env whose edge already carries the backend's public key. A dedicated `409`-until-deployed guard is `(Planned.)` — the backend tracks a per-env provisioning `status` (see [`POST /v1/environments`](#post-v1environments)) but the mint endpoint does not yet gate on it reaching `running`; until it does, the endpoint mints whenever the signer is configured and the environment exists.

**Error behaviour.** Bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers this write. | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (row-level security returns not-found for another tenant's env, never leaking its existence). | Mint for an environment id the caller's tenant owns. |
| `501` | No backend MCP signing key is configured (`ERUN_API_MCP_SIGNING_KEY_PATH` unset). | Configure the signing key on the backend, or use the desktop `file://` path. |
| `500` | The tenant read or the signing failed (e.g. missing request-scoped security context — an internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

### `POST /v1/environments/{environment_id}/dns01-token` {#dns01-token-endpoint}

Mints a per-env **DNS-01 broker token** — the long-lived, backend-signed credential the cluster's cert-manager DNS-01 webhook presents to the [broker](#dns01-broker) to solve ACME challenges within the env's own subzone. Same signing key as the [mcp-token](#mcp-token-endpoint) but a **distinct audience** (`erun-dns01:<tenant>/<environment>`), so the two capabilities cannot be replayed against each other. The environment is resolved from `{environment_id}` under row-level security, so a token can only be minted for the caller's own tenant. Body-less; on success HTTP `200`:

```jsonc
// 200 response
{
  "token": "<eddsa-jwt>",          // long-lived (survives cert renewals); store as the env's dns01-token Secret
  "audience": "erun-dns01:acme/prod"
}
```

The operator lands this token as the Secret the per-tenant Issuer's webhook solver references (see the `erun-enable-hosting-edge` skill). Enabled by the same `ERUN_API_MCP_SIGNING_KEY_PATH` as the mcp-token endpoint; unset → `501`.

**Error behaviour.** Bare HTTP status, plain-text body:

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). `WriteAll` covers this write. | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (RLS returns not-found for another tenant's env). | Mint for an environment id the caller's tenant owns. |
| `501` | No backend signing key is configured (`ERUN_API_MCP_SIGNING_KEY_PATH` unset). | Configure the signing key on the backend. |
| `500` | The tenant read or signing failed (internal wiring error). | Retry; if it persists, it is a server bug. |

### DNS-01 broker: `POST /v1/dns01/present` · `POST /v1/dns01/cleanup` {#dns01-broker}

The DNS-01 broker makes per-tenant cert issuance safe on a **multi-tenant** cluster (issue #818). A shared cluster-scoped `ClusterIssuer` plus one zone-wide TSIG key is an impersonation hole — any namespace could issue any tenant's cert. Instead, each tenant runs a per-tenant namespaced `Issuer` whose DNS-01 challenges route (via a per-cluster cert-manager webhook shim) to this broker, which holds the one TSIG key centrally and **authorizes every write against the caller's own subzone**.

These are **machine-to-machine** endpoints — authenticated by the per-env DNS-01 token (not a user OIDC token), so they sit outside the user-auth middleware. Each request body is `{ "fqdn": "_acme-challenge.…", "value": "<challenge>" }`; the webhook shim marshals cert-manager's `ChallengeRequest` into it and sends the env token as `Authorization: Bearer <jwt>`.

A request succeeds (`204`) only when:

1. `Authorization: Bearer <jwt>` is present and verifies against the backend's DNS-01 signing key — missing/invalid → `401`. The token's audience yields the `(tenant, environment)`; an MCP-audience token is rejected here (wrong audience).
2. The challenge FQDN is an `_acme-challenge` name **within** that env's subzone `<tenant>-<environment>.<services-zone>` — anything else (another tenant's or env's name, a foreign zone, a non-challenge record) → `403`, no write. This is the impersonation guard: `(tenant, environment)` come only from the verified token, never the FQDN.
3. The `_acme-challenge` TXT write to PowerDNS (RFC2136 DNS UPDATE + central TSIG) succeeds — a DNS failure → `502`. Every authorized write is audited.

`present` adds the challenge TXT; `cleanup` removes it. The broker is only registered when the platform env configures the PowerDNS write path (`ERUN_DNS01_*`); otherwise the endpoints are absent. On a hosted deploy those are set (opt-in) by the `erun-backend-api` chart's `api.dns01.{enabled,servicesZone}` values — the TSIG key, algorithm, and PowerDNS `:53` endpoint default to the co-located `erun-powerdns` chart's conventions, so enabling the broker needs only `enabled` + the services zone. Driving this against a **live** two-tenant cluster (staging then production ACME, with the negative cross-tenant test) is the issue's end-to-end acceptance — `(Planned.)` until a second tenant is stood up on the platform cluster.

### Registry token service: `GET /v2/token` {#registry-token-endpoint}

Mints the short-lived, scope-limited access token erun's hosted container registry (`registry.erunpaas.com`, zot) challenges a `docker`/OCI client for — the registry v2 [Bearer token](https://distribution.github.io/distribution/spec/auth/token/) flow. A push or pull gets `401` with `WWW-Authenticate: Bearer realm="…/v2/token",service="registry.erunpaas.com",scope="repository:<name>:<actions>"`; the client then calls `realm` with **HTTP Basic** credentials — a fixed, documented username (`erun`; never inspected, so trusting it would let a caller claim any tenant by naming it) and the tenant's own erun-api bearer token as the password — and gets this endpoint's token back.

```jsonc
// 200 response
{
  "token": "<eddsa-jwt>",
  "access_token": "<eddsa-jwt>",   // duplicates token; some clients read one field name, some the other
  "expires_in": 300,
  "issued_at": "2026-08-24T00:00:00Z"
}
```

**Scope clamping is the security boundary of this endpoint.** The tenant is resolved only from the Basic password's verified issuer — never from the username or the requested scope. Every requested `repository:<name>:<actions>` scope is granted only when `<name>` is the resolved tenant's own namespace (`<tenant>` or `<tenant>/…`); anything else — another tenant's namespace, a name that merely starts with the tenant's (`frs` must not match `frsking`), a non-`repository` resource type — is **dropped entirely**. A request naming no in-scope repository still authenticates (`200`) with an empty `access` grant inside the token, per the token spec's own "grant less than requested, down to nothing" contract — this is deliberate: a distinguishable error here would let a caller probe which tenant namespaces exist.

Same signing key as the [mcp-token](#mcp-token-endpoint) and [dns01-token](#dns01-token-endpoint) endpoints, again with a **distinct audience**: the minted token's `aud` is the registry's own `service` value from the challenge (`registry.erunpaas.com`), never `erun-api` or an `erun-mcp:<tenant>/<env>` value, so it cannot be replayed against the platform API or an env's MCP edge. `iss` is the fixed `erun-registry-token-service` value. Enabled by the same `ERUN_API_MCP_SIGNING_KEY_PATH` as the other two; unset, or no tenant resolver configured (no database), leaves the route **unregistered** (`404`), matching the [DNS-01 broker](#dns01-broker)'s absent-when-unconfigured behaviour rather than the mcp-token endpoint's `501`.

**Error behaviour.** Bare HTTP status, plain-text body:

| Status | Condition | Recovery |
|---|---|---|
| `401` | Missing/malformed Basic credentials, or the password does not verify as a valid, unexpired, correctly-audienced (`erun-api`) bearer token from a trusted issuer. | Send `docker login registry.erunpaas.com` (or an equivalent Basic-auth client) with a current tenant API token as the password. |
| `400` | Missing `service` query parameter. | The registry's own challenge always sets it; a hand-built request must too. |
| `404` | The route is not registered on this instance (no signing key configured, or no database/tenant resolver). | Configure `ERUN_API_MCP_SIGNING_KEY_PATH` and a database on the instance fronting the registry. |
| `500` | Signing failed (internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

**`(Planned.)` end to end.** The endpoint, its scope clamping, and its signing are implemented and unit-tested (`erun-backend-api/internal/registrytoken`) against the real verifier and signer, but no deployed zot instance points its bearer `realm`/`cert` at this endpoint yet — `registry.erunpaas.com`'s DNS, TLS certificate, and the platform's `erun-oci-registry` chart deployment are not yet live (see [Container registries · Hosted registry](/deployment/registries#hosted-registry)).

### `POST /v1/contexts`

Registers a **cloud context** (a managed cluster) for the caller's tenant and returns the cluster-**bootstrap plan**. The model is **BYO-cloud**: the context bootstraps onto the tenant's own AWS account via a registered cloud-provider alias (`cloudProviderAlias`), provisioning an EC2 instance running k3s. The endpoint is tenant-scoped by row-level security — the registered row is bound to the caller's tenant automatically.

```jsonc
// POST /v1/contexts body (BYO-cloud)
{
  "name": "primary",            // required — the context (cluster) name; also its kubernetes context
  "cloudProviderAlias": "aws-acme", // required — a registered AWS cloud-provider alias for the tenant
  "region": "eu-west-2",        // required — AWS region (must be a supported region)
  "instanceType": "c8gd.2xlarge", // optional — defaults applied server-side when empty
  "diskType": "gp3",            // optional
  "diskSizeGb": 100,            // optional
  "preview": false              // optional — when true, returns the plan only (no registration)
}
```

**The `plan`.** Every call resolves the **bootstrap plan** — the ordered EC2/k3s commands the bootstrap would run (security-group + IAM instance-profile setup, `ec2 run-instances`, the k3s install user-data, the kube-context wiring) — by running the bootstrap in **dry-run** against the tenant's alias. It is returned as an array of trace lines so the caller can preview exactly what the live bootstrap will do.

- **`preview: true`** → returns `{ "plan": [ … ] }` only. Nothing is registered.
- **`preview: false`** (default) → registers a context row at status `provisioning`, **kicks off the live bootstrap asynchronously**, and returns `{ "context": { … }, "plan": [ … ] }` with HTTP `202 Accepted`. Poll `GET /v1/contexts/{context_id}` until `status` reaches `running` (success) or `failed`.

```jsonc
// 202 response (preview=false): registered, and provisioning has started
{
  "context": {
    "contextId": "019a7fa5-c2c0-7c55-bc70-714873a71f20",
    "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
    "name": "primary",
    "provider": "aws",
    "cloudProviderAlias": "aws-acme",
    "region": "eu-west-2",
    "kubernetesContext": "primary",
    "status": "provisioning",   // provisioning → running (success) | failed
    "createdAt": "2026-06-24T10:00:00Z",
    "updatedAt": "2026-06-24T10:00:00Z"
  },
  "plan": [
    "aws ec2 create-security-group …",
    "aws ec2 run-instances …",
    "kubectl config set-cluster primary …"
  ]
}
```

**Async, durable provisioning (issue #605).** The live bootstrap runs as a **durable DBOS workflow** — it survives a control-plane restart, resuming from its last completed step. It executes the real (non-dry-run) `InitCloudContext` against the tenant's BYO-cloud alias (security group + IAM instance-profile, `run-instances` with the k3s install user-data, `wait`, resolve the public IP), driving EC2, IAM, and SSM **in-process through the AWS SDK** — the control-plane image ships no `aws`, `kubectl`, or `helm` binary — then takes **server-side custody of the k3s admin token** — encrypted at rest in `context_credentials`, never returned — and sets the context `status`:

- `provisioning` → in flight.
- `running` → the cluster is up; `instanceId` and `publicIp` are populated.
- `failed` → `provisionError` carries the reason.

`GET /v1/contexts/{context_id}` returns the current `status` (plus `provisionError` when failed). The k3s admin token is a **server secret** and is never part of any response or the read model.

**Prerequisites.** Live provisioning requires (1) a registered cloud-provider alias holding the tenant's encrypted BYO-cloud credentials (`PUT /v1/cloud-provider-aliases/{alias}`, below), and (2) the platform configured with a DBOS system database (`DBOS_SYSTEM_DATABASE_URL`) and a secrets key (`ERUN_SECRETS_KEY`). When provisioning is **not** configured, `POST /v1/contexts` registers the row and returns `201` with the plan only (no live bootstrap) — the pre-#676 behaviour.

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name`, `cloudProviderAlias`, or `region` is empty/missing, or the body is not valid JSON. | Send all three required fields. |
| `400` | The bootstrap plan could not be resolved (e.g. an unsupported `region`, `instanceType`, or `diskSizeGb` for the BYO-cloud bootstrap). | Use a supported region/instance type/disk size. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/contexts`. | Send a valid token whose roles permit the write. |
| `500` | Persistence failed (e.g. missing request-scoped security context — an internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

### `PUT /v1/cloud-provider-aliases/{alias}`

Registers (upserts) the caller tenant's **BYO-cloud credentials** under a named alias — the secret the provisioning executor resolves to talk to the tenant's cloud (issue #605). The blob is **opaque to this endpoint** (stored and validated only as a non-empty string) and is **encrypted at rest**: the `credentials_encrypted` column never holds plaintext. Tenant-owned (row-level security binds the alias to the caller), so any authorized tenant manages its own aliases; no operations gate.

```jsonc
// PUT /v1/cloud-provider-aliases/{alias} body
{
  "provider": "aws",   // optional — defaults to aws; must be aws today
  "credentials": "{\"accessKeyId\":\"…\",\"secretAccessKey\":\"…\",\"sessionToken\":\"…\"}" // required — encrypted at rest
}
```

Returns `204 No Content`. Available only when the platform is configured with a secrets key (`ERUN_SECRETS_KEY`).

**What the AWS executor requires of the blob.** The provisioning executor parses it as JSON and uses those keys as the **only** identity it acts as:

| Key | Required | Meaning |
|---|---|---|
| `accessKeyId` | yes | AWS access key id. |
| `secretAccessKey` | yes | AWS secret access key. |
| `sessionToken` | no | Session token for temporary credentials; omit for long-lived keys. |

There is **no fallback to an ambient credential chain**. A blob that is not JSON, or that omits either required key, fails the provision immediately with `provisionError` naming the alias — it does not fall through to a shared config file, an instance profile, or a web-identity role the control plane itself might hold, because a tenant's provisioning must never act as another identity. Temporary credentials must outlast the bootstrap: they are not refreshed mid-workflow, so an expired token surfaces as an AWS authentication failure in `provisionError`.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `400` | `alias` path value empty, `credentials` empty, or `provider` is not `aws`. | Send a non-empty alias + credentials with `provider: aws`. |
| `401` / `403` | Standard auth failures; `WriteAll` covers the `PUT`. | Send a valid token whose roles permit the write. |

### `POST /v1/provision`

Returns the complete, ordered **plan** to provision a hosted env for the caller's tenant — the single auditable preview a console or Operator sees before provisioning. It **composes the same dry-run primitives** the discrete endpoints expose into one ordered plan: the per-tenant environment-count quota check from [`POST /v1/environments`](#post-v1environments), the cluster-**bootstrap plan** from [`POST /v1/contexts`](#post-v1contexts), the `<tenant>-<env>` namespace creation, the environment registration, and the runtime-chart deploy. The endpoint is tenant-scoped by the token's security context — the tenant is **resolved from the token, never from the body**.

This endpoint is **preview-only** in this build: it resolves and shows the concrete actions but **never executes** the plan and **never writes** to the database. The discrete `POST /v1/contexts` and `POST /v1/environments` endpoints own the config writes; this endpoint composes their plans with no side effects.

```jsonc
// POST /v1/provision body
{
  "environment": {                  // required
    "name": "prod",                 // required — DNS-1123 label; forms the <tenant>-<env> namespace
    "type": "runtime"               // required — one of runtime, remote-agent, local-agent
  },
  "context": {                      // optional — present when provisioning a NEW cluster
    "name": "acme-prod",            // required when context is present — the cluster (and kube-context) name
    "cloudProviderAlias": "acme-aws", // required when context is present — a registered AWS alias for the tenant
    "region": "eu-west-2",          // required when context is present — AWS region
    "instanceType": "c8gd.2xlarge", // optional
    "diskType": "gp3",              // optional
    "diskSizeGb": 100               // optional
  },
  "kubernetesContext": "acme-prod"  // optional — reference an EXISTING context instead of bootstrapping a new cluster
}
```

Provide **either** a `context` block (provision a new cluster — its bootstrap plan is the real `InitCloudContext` dry-run argv) **or** a `kubernetesContext` (reuse an existing context by raw name). When a `context` block is present it wins and `kubernetesContext` is ignored. **For a `runtime` environment, leave both unset**: this endpoint refuses either with `400` before building a plan — it has no `contextId` field of its own and only ever names a cluster by raw string, which [`POST /v1/environments`](#post-v1environments)'s [placement](/concepts/hosted-platform#single-cluster-placement) resolution does not accept either, so refusing here keeps this preview from promising something the executing path would then refuse. This preview does **not** yet cover placing a `runtime` environment onto an already-registered `contextId` — use `POST /v1/environments {"preview": true}` for that. `remote-agent`/`local-agent` environments — never server-side deployed — may still preview a `context`/`kubernetesContext` freely.

**The ordered `plan`.** The response is `{ "plan": [ … ], "quotaOk": <bool> }`. `plan` is the human-readable, audit-style ordered list of every action the live provision would take, in this exact order:

1. **authz/tenant** — `provision: tenant <tenant> (resolved from token)`.
2. **quota** — `quota: tenant has <count> of <cap> environments` followed by ` — within quota` or ` — WOULD EXCEED, provisioning blocked`. The cap is the tenant's `tenant_quotas.max_environments` (default `10`); both reads are row-level-security-scoped to the caller's tenant. `<count>` excludes environments at `deleting`/`deletion-blocked`, matching what `POST /v1/environments` admission itself counts. For a `runtime` environment, two more quota lines follow — these use the tenant's runtime-environment count as-is, which does **not** apply that exclusion: `quota: namespace capped at <cpu>m CPU / <mem>Mi memory / <storage>Gi storage` (the per-environment ceiling), then `quota: <n> runtime environment(s) at that cap project to <cpu>m CPU / <mem>Mi memory / <storage>Gi storage against a tenant budget of <totalCpu>m / <totalMem>Mi / <totalStorage>Gi` followed by ` — within budget` or ` — WOULD EXCEED, provisioning blocked` (the aggregate tenant-wide budget, #1113; see [Quotas](/concepts/hosted-platform#quotas)).
3. **placement** — one of three lines, depending on the request: `context: bootstrap cluster <name> via alias <alias>` (a `context` block was given — non-runtime only) followed by the full `InitCloudContext` dry-run argv, exactly the plan [`POST /v1/contexts`](#post-v1contexts) returns; `context: reuse existing kubernetes context <kubernetesContext>` (non-runtime only); or, for a `runtime` environment with neither set, `context: deploys into this platform's own cluster (v1 single-cluster placement)`. A non-runtime environment with neither set gets `context: none (not server-side deployed)`.
4. **namespace** — `namespace: would create <tenant>-<env>`.
5. **register** — `register: would persist environment <name> (<type>) in tenant <tenant> referencing context <ref>` (`<ref>` is empty for the platform's-own-cluster and none cases above).
6. **deploy** — `deploy: would helm install the erun-devops runtime chart (release <tenant>-devops) into <tenant>-<env>`. Present **only for a `runtime` environment** — a `remote-agent`/`local-agent` plan ends at the register line, since the platform never server-side deploys them.
7. **auth** — `auth: would inject this backend's MCP-signing public key so the runtime's MCP edge trusts tokens minted for the console (skipped when the backend has no MCP signing key configured)`. Present only alongside the deploy line; see [Per-env MCP edge authentication](#mcp-edge).
8. **expose** — `expose: would wire mcp.<tenant>-<env>.<services zone> via a per-env wildcard DNS record and Host-routing Ingress (skipped when the platform has no services zone configured)`. Present only alongside the deploy line; see [Automatic exposure](/concepts/hosted-platform#automatic-exposure) for when the live deploy actually performs this and when it safely skips it.
9. **tls** — `tls: would provision a per-env wildcard certificate through the DNS-01 broker (skipped when the platform has no ACME email or DNS-01 broker configured)`. Present only alongside the deploy line; see [Per-env TLS certificate provisioning](/concepts/hosted-platform#per-env-tls).

**`quotaOk`.** `true` when the provision fits under the tenant's environment-count cap **and** (for a `runtime` environment) its aggregate resource budget, `false` when either would be exceeded. When `quotaOk` is `false` the endpoint **still returns the full plan** with HTTP `200` (it is a preview, not a write), and the relevant quota line names the block — surfacing the blocking decision the way a dry-run does, rather than rejecting with a `409`. A caller gating on the quota should check `quotaOk`, not the status code.

```jsonc
// 200 response — runtime environment, no context (the only valid shape for runtime in v1), within quota
{
  "plan": [
    "provision: tenant acme (resolved from token)",
    "quota: tenant has 2 of 10 environments — within quota",
    "quota: namespace capped at 8000m CPU / 17832Mi memory / 72Gi storage",
    "quota: 2 runtime environment(s) at that cap project to 16000m CPU / 35664Mi memory / 144Gi storage against a tenant budget of 80000m / 178320Mi / 720Gi — within budget",
    "context: deploys into this platform's own cluster (v1 single-cluster placement)",
    "namespace: would create acme-prod",
    "register: would persist environment prod (runtime) in tenant acme referencing context ",
    "deploy: would helm install the erun-devops runtime chart (release acme-devops) into acme-prod",
    "auth: would inject this backend's MCP-signing public key so the runtime's MCP edge trusts tokens minted for the console (skipped when the backend has no MCP signing key configured)",
    "expose: would wire mcp.acme-prod.<services zone> via a per-env wildcard DNS record and Host-routing Ingress (skipped when the platform has no services zone configured)",
    "tls: would provision a per-env wildcard certificate through the DNS-01 broker (skipped when the platform has no ACME email or DNS-01 broker configured)"
  ],
  "quotaOk": true
}
```

```jsonc
// 200 response — remote-agent environment bootstrapping a new cluster (never server-side deployed, so no deploy/auth line)
{
  "plan": [
    "provision: tenant acme (resolved from token)",
    "quota: tenant has 2 of 10 environments — within quota",
    "context: bootstrap cluster acme-prod via alias acme-aws",
    "aws ec2 create-security-group …",
    "aws ec2 run-instances …",
    "kubectl config set-context acme-prod …",
    "namespace: would create acme-staging",
    "register: would persist environment staging (remote-agent) in tenant acme referencing context acme-prod"
  ],
  "quotaOk": true
}
```

**This endpoint itself only resolves and returns the plan — it never executes it or writes to the database.** But the discrete endpoints it composes are executing paths in their own right: [`POST /v1/environments`](#post-v1environments) (with `preview: false`, the default) really registers the row and, for a pinned `runtime` environment, really starts the deploy — which, per [Automatic exposure](/concepts/hosted-platform#automatic-exposure), also chains the expose line above when the platform is configured for it; [`POST /v1/environments/{id}/deploy`](#deploy-endpoint), [`.../stop`](#stop-endpoint), and [`DELETE`](#delete-endpoint) really run their Jobs (`deploy` and `DELETE` asynchronously, behind a durable workflow); [`POST /v1/contexts`](#post-v1contexts) (with `preview: false`) really bootstraps a cluster.

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `environment.name` is not a DNS-1123 label, `environment.type` is not one of `runtime`/`remote-agent`/`local-agent`, a `context` block is present but missing `name`/`cloudProviderAlias`/`region`, or the body is not valid JSON. | Send a valid `environment` and, if provisioning a new cluster, a complete `context` block. |
| `400` | The context bootstrap plan could not be resolved (e.g. an unsupported `region`, `instanceType`, or `diskSizeGb`). | Use a supported region/instance type/disk size. |
| `400` | A `runtime` environment named a `context` block or `kubernetesContext` (see [Placement](/concepts/hosted-platform#single-cluster-placement) above). | Leave both unset for a `runtime` environment. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/provision`. | Send a valid token whose roles permit the write. |
| `500` | The tenant or quota read failed (e.g. missing request-scoped security context — an internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

Note that being **at or over quota is not an error** here — it returns `200` with `quotaOk: false` and the full plan (see `quotaOk` above), unlike `POST /v1/environments`, which rejects the actual write with `409`.

### `POST /v1/tenants`

Registers a **new tenant** plus the OIDC issuer mapping that resolves its tokens. This is an **operations-only** endpoint: beyond the broad `WriteAll` permission that authorization enforces for any write, the handler adds an explicit gate — the caller's resolved tenant must be an `OPERATIONS` tenant, because `tenants`, `issuers`, and `tenant_issuers` are root resolution tables writable only by the operations role. A non-operations caller is rejected with `403` before any write is attempted.

```jsonc
// POST /v1/tenants body (operations-only)
{
  "name": "acme",                  // required — lowercase letters and digits only, NO hyphens (globally unique)
  "type": "COMPANY",               // optional — "COMPANY" (default) or "OPERATIONS"
  "issuer": "https://idp.example", // required — the OIDC issuer whose tokens map to this tenant
  "orgFieldKey": "org_id",         // optional — org-scoped (shared) issuer: the token claim carrying the org
  "orgFieldValue": "42",           // optional — org-scoped issuer: the org value that selects this tenant
  "displayName": "Acme IdP"        // optional — display name for the issuer mapping (defaults to the issuer URL)
}
```

The three identity rows — the `tenants` row, the `issuers` registry row (the globally unique issuer key with its org-scoping mode), and the `tenant_issuers` mapping row binding the issuer (and org value, when org-scoped) to the new tenant — are inserted in **one transaction**. `orgFieldKey`/`orgFieldValue` are set only for an org-scoped (shared) issuer; a single-tenant issuer leaves both empty (NULL on the registry / mapping). No first user is created here: the tenant's first admin is enrolled by the per-tenant first-user bootstrap when the tenant's first valid token arrives (see [first-identity bootstrap](#tenant-issuers)).

```jsonc
// 201 response
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f50",
  "name": "acme",
  "type": "COMPANY",
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name` is empty or contains anything other than lowercase letters and digits (no hyphens — so the `<tenant>-<env>` namespace stays injective), `issuer` is empty/missing, `type` is not one of `COMPANY`/`OPERATIONS`, or the body is not valid JSON. | Send a hyphen-free lowercase-alphanumeric `name`, a non-empty `issuer`, and a valid `type`. |
| `403` | The caller's resolved tenant is not an `OPERATIONS` tenant (the explicit operations gate, beyond the standard auth failures in [Errors](#errors)). | Call from an operations-tenant token whose roles permit the write. |
| `500` | Persistence failed — e.g. the tenant `name` or the `(issuer, org_field_value)` mapping already exists (a uniqueness violation), or the request-scoped security context is missing (an internal wiring error). | Use a unique tenant name and issuer mapping; if it persists with unique inputs, it is a server bug. |

### `POST /v1/users` and `GET /v1/users` {#post-v1users-and-get-v1users}

Enrolls or lists users. Today the **only** other way a user comes to exist is the per-tenant first-user bootstrap (see [above](#tenant-issuers)) — this endpoint is how an authorized caller enrolls additional users beyond that first one.

Both act on the caller's own resolved tenant by default. An explicit `tenantId` (body field for the `POST`, `?tenantId=` query param for the `GET`) targets a **different** tenant, and is honored only when the caller's resolved tenant is `OPERATIONS` — the same cross-tenant precedent as [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota): a non-operations caller naming another tenant is rejected with `403` before any read or write.

```jsonc
// POST /v1/users body
{
  "username": "alice",              // required
  "issuer": "https://idp.example",  // optional — links the external identity so the enrollee can actually sign in
  "subject": "alice@idp.example",   // optional — required together with issuer
  "tenantId": "019a…"               // optional — operations-only cross-tenant target
}

// 201 response
{
  "userId": "019a7fa5-c2c0-7c55-bc70-714873a71f60",
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "username": "alice",
  "issuer": "https://idp.example",
  "subject": "alice@idp.example",
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

Omitting `issuer`/`subject` enrolls a username with **no external identity yet** — the row exists, but no token can resolve to it until one is linked, and there is no separate endpoint to link one after the fact in this build. Enrollment grants the same predefined `ReadAll`/`WriteAll` roles every bootstrapped user gets — this is the safe default for a tenant's first user (see [empty-database bootstrap](#tenant-issuers) above), not the only role shape a tenant can hold. [`GET`/`POST /v1/roles`](#roles-endpoints) and [`/v1/users/{user_id}/roles`](#roles-endpoints) below are how an operator narrows a user's access after enrollment.

This endpoint requires the caller to already know the enrollee's `issuer`/`subject` from the identity provider. [`POST /v1/identity/users`](/agent-reference/identity-administration) is the higher-level alternative for a platform running its own IdP (Zitadel): it creates the IdP identity itself and calls this same mapping with the subject the IdP returns, in one action, restricted to an `OPERATIONS` tenant.

```jsonc
// GET /v1/users?tenantId=019a… (operations-only cross-tenant; omit tenantId for the caller's own tenant)
// 200 response
[
  {
    "userId": "019a7fa5-c2c0-7c55-bc70-714873a71f60",
    "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
    "username": "alice",
    "issuer": "https://idp.example",
    "subject": "alice@idp.example",
    "createdAt": "2026-06-24T10:00:00Z",
    "updatedAt": "2026-06-24T10:00:00Z"
  }
]
```

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `username` is empty, or the body is not valid JSON. | Send a non-empty `username`. |
| `403` | `tenantId` (or `?tenantId=`) names a different tenant than the caller's own, and the caller's resolved tenant is not `OPERATIONS`. | Omit `tenantId` to act on your own tenant, or call from an operations-tenant token. |
| `409` | `POST /v1/users`: a user with that `username` already exists in the target tenant (`users_tenant_username_key`). | Use a different username, or omit `tenantId` if you meant your own tenant. |

### Roles and role assignment {#roles-endpoints}

A **role** is a named, tenant-owned bundle of permissions; a **permission** is one API method + path grant, either an exact pair (`apiMethod`/`apiPath`) or a regex pattern pair (`apiMethodPattern`/`apiPathPattern`) — the same shape `role_permissions` stores and [the capability set](#capability-set) resolves against. `ReadAll` and `WriteAll` are two such roles, predefined per tenant and granted to every bootstrapped user; a tenant can also define its own narrower roles and assign them instead. All five endpoints below act on the caller's own resolved tenant — RLS scopes every read and write, and there is no operations-tenant cross-tenant override (unlike `/v1/users`).

**`GET /v1/roles`** lists the tenant's roles with their permissions. **`POST /v1/roles`** creates one:

```jsonc
// POST /v1/roles body
{
  "name": "ReviewsReader",
  "permissions": [
    { "apiMethod": "GET", "apiPath": "/v1/reviews" },
    { "apiMethodPattern": "^GET$", "apiPathPattern": "^/v1/reviews/.*$" }
  ]
}

// 201 response
{
  "roleId": "019a7fa5-c2c0-7c55-bc70-714873a71f70",
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "name": "ReviewsReader",
  "permissions": [
    { "rolePermissionId": "019a7fa5-…", "tenantId": "019a7fa5-…", "roleId": "019a7fa5-…", "apiMethod": "GET", "apiPath": "/v1/reviews", "createdAt": "…", "updatedAt": "…" },
    { "rolePermissionId": "019a7fa5-…", "tenantId": "019a7fa5-…", "roleId": "019a7fa5-…", "apiMethodPattern": "^GET$", "apiPathPattern": "^/v1/reviews/.*$", "createdAt": "…", "updatedAt": "…" }
  ],
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

Each permission needs **exactly one** of the exact pair or the pattern pair (matching `role_permissions_exact_or_pattern_check`); an exact `apiMethod` must be one of `GET`/`HEAD`/`OPTIONS`/`POST`/`PUT`/`PATCH`/`DELETE`, and both pattern fields must compile as regular expressions. At least one permission is required — a role with none could never be granted meaningfully.

**`GET /v1/users/{user_id}/roles`** lists a user's assigned roles. **`POST /v1/users/{user_id}/roles`** grants one; **`DELETE /v1/users/{user_id}/roles/{role_id}`** revokes one:

```jsonc
// POST /v1/users/019a7fa5-…-f60/roles body
{ "roleId": "019a7fa5-c2c0-7c55-bc70-714873a71f70" }

// 201 response
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "userId": "019a7fa5-c2c0-7c55-bc70-714873a71f60",
  "roleId": "019a7fa5-c2c0-7c55-bc70-714873a71f70",
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

**The lockout guard.** `DELETE /v1/users/{user_id}/roles/{role_id}` refuses when the revoke would leave the tenant with **no user holding a role that can grant roles** (a permission matching `POST /v1/users/{user_id}/roles` itself) — the one failure this feature makes impossible rather than merely recoverable, since there would be no lever left inside the product to undo it. The check runs against every user in the tenant, not just the one being revoked, so revoking a role from one admin while another admin still holds a grant-capable role succeeds normally.

**Error behaviour.** Bare HTTP status, plain-text body, same as `/v1/users`:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `POST /v1/roles`: `name` is empty, no permissions given, or a permission fails the exact/pattern-exclusivity, method-enum, or regex-compile checks above. `POST /v1/users/{user_id}/roles`: `roleId` is empty. | Fix the request body per the rules above. |
| `404` | The `role_id` or `user_id` does not exist in the caller's tenant (a cross-tenant reference is invisible under RLS, not merely forbidden). | Use an ID that belongs to your own tenant. |
| `409` | `POST /v1/roles`: a role with this name, or one of its permissions, already exists in the tenant. `POST /v1/users/{user_id}/roles`: the user already holds this role. `DELETE .../roles/{role_id}`: the lockout guard above refused the revoke. | Use a different name, or accept the existing grant; for the lockout case, grant a recovery role to another user (or the same user) before revoking this one. |

### `POST /v1/invites`, `GET /v1/invites`, `DELETE /v1/invites/{invite_id}` {#invites}

Registration is invite-only (issue #1483): self-registration on the platform's IdP is closed via [`allowRegister`](/agent-reference/identity-administration#org-settings), and this is the replacement path for adding a member. An invite is a **server-side record** — revocable and listable up until it is accepted — not a self-contained signed token; the token field below is the only part of it that ever leaves the backend.

Create and list act on the caller's own resolved tenant by default. An explicit `tenantId` (body field for the `POST`, `?tenantId=` query param for the `GET`) targets a **different** tenant, honored only when the caller's resolved tenant is `OPERATIONS` — the same cross-tenant precedent [`POST`/`GET /v1/users`](#post-v1users-and-get-v1users) uses. This is deliberate for an invite whose target is an `OPERATIONS` tenant: accepting it lands the invitee with `erun_operations` database access across every tenant, so minting one needs the same operations-only gate as any other cross-tenant action today. A distinct permission scoped specifically to "invite into an OPERATIONS tenant" (rather than reusing the coarser operations-tenant-membership check) is now expressible: the role-assignment layer has since shipped, so this coarser tenant-type gate is a deliberate carry-over to be narrowed, not a missing capability.

```jsonc
// POST /v1/invites body — every field optional
{
  "email": "bob@example.com",   // optional — pins the invite to one email; accept refuses a different one
  "tenantId": "019a…"           // optional — operations-only cross-tenant target
}

// 201 response
{
  "inviteId": "019a…",
  "tenantId": "019a…",
  "token": "kX92n…",             // the invite's credential — build the accept link from this
  "email": "bob@example.com",
  "expiresAt": "2026-09-03T16:00:00Z",   // fixed 7-day TTL, not caller-configurable today
  "createdAt": "2026-08-27T16:00:00Z",
  "updatedAt": "2026-08-27T16:00:00Z"
}
```

```jsonc
// GET /v1/invites?tenantId=019a… (operations-only cross-tenant; omit tenantId for the caller's own tenant)
// 200 response — only outstanding (unconsumed) invites, newest first
[
  {
    "inviteId": "019a…",
    "tenantId": "019a…",
    "token": "kX92n…",
    "email": "bob@example.com",
    "expiresAt": "2026-09-03T16:00:00Z",
    "createdAt": "2026-08-27T16:00:00Z",
    "updatedAt": "2026-08-27T16:00:00Z"
  }
]
```

`DELETE /v1/invites/{invite_id}` revokes it; `204` on success. There is no soft-revoke state — a revoked invite's row is gone, so a stale accept attempt against it resolves the same as an unknown token.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `400` | Body is not valid JSON. | Send a valid JSON object; every field is optional. |
| `403` | `tenantId` (or `?tenantId=`) names a different tenant than the caller's own, and the caller's resolved tenant is not `OPERATIONS`. | Omit `tenantId` to act on your own tenant, or call from an operations-tenant token. |
| `404` | `DELETE`: `invite_id` does not name an outstanding invite in a tenant the caller can reach. | Re-check the id from `GET /v1/invites`; an already-consumed or already-revoked invite is not found here. |

### `POST /v1/invites/accept` {#invites-accept}

Consumes an invite token and enrolls the invitee as an erun user of the invite's target tenant. **Unauthenticated** — registered directly on the mux like [`GET /v1/platform`](#platform-endpoint) — because the invitee has no bearer token yet; the invite token in the body is what authorizes this call, and it is single-use and validated atomically (a `SELECT ... FOR UPDATE` against the invite row) so two concurrent accepts of the same token cannot both succeed.

Unlike [`POST /v1/identity/users`](/agent-reference/identity-administration) (an operator composing an account for someone absent, which never sets a caller-supplied password so no operator ever handles a credential belonging to someone else's account), the invitee is present and choosing their own password right now — there is no "someone else's account" to protect them from, and Zitadel's own email-invite flow would be the wrong choice regardless, since its link has nothing to do with the credential just supplied. So this always creates the IdP identity with the supplied password as its initial password, marking the email verified and landing the account `USER_STATE_ACTIVE` immediately — no email round-trip, and it works with no SMTP configured at all (issue #1168). A future increment may prefer WebAuthn/passkey registration here instead of a password, to keep the "never handle someone else's credential" property even tighter; that is not implemented today, and `password` is required.

```jsonc
// request body
{
  "token": "kX92n…",              // required
  "username": "bob",              // required
  "email": "bob@example.com",     // optional; required to match the invite's pinned email, if it set one
  "firstName": "Bob",             // optional
  "lastName": "Operator",         // optional
  "password": "the-invitee-chose-this"   // required
}

// 201 response — both halves landed
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_ACTIVE" },
  "erunUser": { "userId": "019a…", "username": "bob" }
}

// 201 response — the IdP identity was created, but the erun mapping failed
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_ACTIVE" },
  "error": "identity created in the identity provider but the erun user mapping failed: idp user id 387728445393600515: a user with this username already exists in the target tenant"
}
```

The half-landed-failure shape mirrors `POST /v1/identity/users` exactly: a failure after the IdP identity exists is a `201` with `error` set and no `erunUser`, not an opaque failure — the console tells the invitee to ask an operator to finish the enrollment rather than retry blindly.

**Error behaviour.** Each of the three invalid-token states is reported distinctly rather than as one generic failure — a stale link should say why it's stale:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `token`/`username`/`password` empty, the body is not valid JSON, or `email` was supplied and does not match the invite's pinned email (case-insensitive). | Send all three required fields; match the pinned email exactly or omit it. |
| `404` | `token` does not name any invite that ever existed (or it was revoked — revocation deletes the row). | Ask whoever invited you for a new link. |
| `410` | The invite exists but has expired, or has already been consumed (single-use). | Ask whoever invited you for a new link. |
| Forwarded from Zitadel | The IdP identity creation itself failed (e.g. the password does not meet the org's complexity policy). | The response body carries Zitadel's own message; act on it directly. |

**Not audited.** Unlike `POST`/`GET`/`DELETE /v1/invites` above (which run through the authenticated middleware that writes `audit_events` for every protected request), this endpoint is registered outside that middleware — the same as [`GET /v1/platform`](#platform-endpoint) — because there is no authenticated caller identity to attribute the row to. The invite's own `created_by_user_id` plus its `consumed_at` timestamp is today's record of who accepted it and when; a dedicated audit event for acceptance is a reasonable follow-up, not yet implemented.

### `PUT /v1/tenants/{tenant_id}/quota` {#put-v1tenantstenant_idquota}

Sets a tenant's full quota row — the environment-count cap the [`POST /v1/environments`](#post-v1environments) quota guardrail enforces, the per-environment CPU/memory/storage namespace ceiling, and the aggregate tenant-wide CPU/memory/storage budget (#1113). **Operations-only**, like tenant registration: the caller's resolved tenant must be `OPERATIONS`, because it writes another tenant's `tenant_quotas` row (the operations role's RLS policy permits cross-tenant writes; the row's `tenant_id` is set explicitly to the path's `{tenant_id}`, not the operations caller's own tenant). The write **fully replaces** the row — it is not a merge, so every field is required on every call.

```jsonc
// PUT /v1/tenants/019a7fa5-…/quota body
{
  "maxEnvironments": 50,          // required — the env-count cap (>= 0); 0 blocks all new environments
  "maxCpuMillicores": 8000,       // required — per-environment namespace CPU ceiling in millicores (> 0)
  "maxMemoryMb": 17832,           // required — per-environment namespace memory ceiling in MiB (> 0)
  "maxStorageGb": 72,             // required — per-environment namespace storage ceiling in GiB (> 0)
  "maxTotalCpuMillicores": 80000, // required — aggregate tenant-wide CPU budget in millicores (> 0)
  "maxTotalMemoryMb": 178320,     // required — aggregate tenant-wide memory budget in MiB (> 0)
  "maxTotalStorageGb": 720        // required — aggregate tenant-wide storage budget in GiB (> 0)
}

// 200 response
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f50",
  "maxEnvironments": 50,
  "maxCpuMillicores": 8000,
  "maxMemoryMb": 17832,
  "maxStorageGb": 72,
  "maxTotalCpuMillicores": 80000,
  "maxTotalMemoryMb": 178320,
  "maxTotalStorageGb": 720,
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:05:00Z"
}
```

**What the resource caps mean.** `maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb` are a **per-environment namespace ceiling**, not an aggregate tenant budget: every `runtime` environment this tenant provisions gets its own Kubernetes `ResourceQuota` + `LimitRange` capped at these same values (see [Quotas](/concepts/hosted-platform#quotas)), so a tenant with ten environments can use up to this cap in *each* of the ten namespaces, not this cap split across all ten. `maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb` are the separate **aggregate tenant-wide budget**: since every environment gets the identical per-environment cap, admission projects `(existing runtime environment count + 1) × the per-environment cap` against this budget and refuses a create that would exceed it (a redeploy uses the count as-is, since it does not add one). Absent a `tenant_quotas` row, a tenant gets the default cap: `maxEnvironments: 10`, `maxCpuMillicores: 8000`, `maxMemoryMb: 17832`, `maxStorageGb: 72` — sized to fit the `erun-devops` chart's own default runtime pod summed across **both** its containers (`erun-devops` cpu limit `4` + memory limit `8916Mi`, plus the `erun-dind` sidecar at the same limits) plus its three default PVCs (`2Gi + 50Gi + 20Gi = 72Gi`) — and `maxTotalCpuMillicores: 80000`, `maxTotalMemoryMb: 178320`, `maxTotalStorageGb: 720` (`maxEnvironments` × the per-environment defaults, so the default budget accommodates the default environment-count cap at the default per-environment size). Setting either resource cap below this floor is accepted here (an operator may deliberately want a tenant that cannot provision runtime environments yet), but the next [`POST /v1/environments`](#post-v1environments) or [`POST .../deploy`](#deploy-endpoint) for that tenant then refuses with `409` rather than letting the create/deploy proceed toward a pod Kubernetes will never admit.

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `tenant_id` is empty, `maxEnvironments` is negative, any of `maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb`/`maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb` is `<= 0` (a PUT replaces the whole row, so these must be sent explicitly on every call), or the body is not valid JSON. | Send a non-negative `maxEnvironments` and positive resource caps. |
| `403` | The caller's resolved tenant is not an `OPERATIONS` tenant (the explicit operations gate). | Call from an operations-tenant token. |

### `GET /v1/quota` {#get-v1quota}

Returns the caller's own tenant's full quota row — the identical shape and defaulting [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) writes and [`POST /v1/environments`](#post-v1environments) admission itself reads. **Tenant-self-service**: no operations role required, RLS scopes the read to the caller's own tenant. This is how an Operator inspects their own limits without an operations-scoped token (#605, #1113).

```jsonc
// 200 response — identical shape to the PUT response above
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f50",
  "maxEnvironments": 10,
  "maxCpuMillicores": 8000,
  "maxMemoryMb": 17832,
  "maxStorageGb": 72,
  "maxTotalCpuMillicores": 80000,
  "maxTotalMemoryMb": 178320,
  "maxTotalStorageGb": 720,
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:05:00Z"
}
```

**Error behaviour.** Standard auth failures only (see [Errors](#errors)); `ReadAll` covers `GET /v1/quota`. There is no `404` — an absent `tenant_quotas` row resolves to the same defaulted values `PUT`/admission would use.

### `GET /v1/usage-events` {#get-v1usage-events}

Lists the caller's tenant's metering events, most recent first — the usage-metering hook for hosted environments. Read-only: events are recorded automatically by the provisioning/lifecycle workflows on a successful deploy, stop, or delete, never written directly by a route.

```jsonc
// 200 response
[
  {
    "usageEventId": "019a7fa5-c2c0-7c55-bc70-714873a71f60",
    "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
    "environmentId": "019a7fa5-c2c0-7c55-bc70-714873a71f30",
    "eventType": "environment_provisioned",   // "environment_provisioned" | "environment_stopped" | "environment_deleted"
    "cpuMillicores": 8000,    // the namespace cap applied at the time — only "environment_provisioned" carries these
    "memoryMb": 17832,
    "storageGb": 72,
    "createdAt": "2026-06-24T10:00:00Z"
  }
]
```

`environmentId` is set `null` if the environment was later deleted — the append-only metering trail outlives the row it described (the FK is `ON DELETE SET NULL`, not a hard reference), so `eventType`/`createdAt`/the tenant's own scope still say what happened even after the environment is gone. `environment_stopped`/`environment_deleted` events carry no resource-cap fields, since they do not apply a namespace cap.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). | Send a valid token. |

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
- [Container registries · Hosted registry](/deployment/registries#hosted-registry) — the Operator-facing summary of the hosted registry this endpoint authenticates.
