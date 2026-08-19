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

- **`https://` OIDC issuer** (the platform's Zitadel, AWS) — verified via the issuer's JWKS through provider discovery, using the same shared verifier the REST API uses (so the API and every MCP edge verify identically). A caller that already holds an `erun-mcp`-audience OIDC token presents it directly. A deploy trusts an OIDC issuer by setting the env's [`mcpauthissuer`](/reference/configuration#envconfig) to it; `erun deploy` threads it into the chart as `mcpAuth.{enabled,issuer,audience}` — issuer verbatim, audience derived as `erun-mcp:<tenant>/<environment>` — and injects **no** key, since the edge fetches the issuer's JWKS. Mutually exclusive with the `file://` key below; the `--mcp-auth-public-key` flag wins when both are set. The **console does not use this path** — a browser SPA cannot obtain an `erun-mcp`-audience OIDC token — it uses the backend-signed `file://` path below (issue #685).
- **`file://` key** (issues #655, #686) — a self-contained trust anchor instead of an OIDC IdP: an Ed25519 public key injected into the runtime pod when the env's runtime is deployed (`erun deploy --mcp-auth-public-key <key>`, or `erun init --mcp-auth-public-key <key>`, which folds it into init's create-time deploy so the desktop needs no separate post-init redeploy). The signer stamps a `file://<path>` `iss` naming that public key; the edge loads the key from that path and verifies the EdDSA signature, with `alg` hard-locked to `EdDSA` (closing the alg-confusion class). Two signers use it, and each env picks exactly one:
  - **Desktop** — the desktop generates the key (`desktopid.key`) once and signs each token locally.
  - **Hosted (console)** — the **backend** is the signer, the hosted twin of the desktop: it holds the MCP signing key (`ERUN_API_MCP_SIGNING_KEY_PATH`) and mints a per-env token on the console's behalf via [`POST /v1/environments/{id}/mcp-token`](#mcp-token-endpoint), while the deploy injects the backend's public key — so the console never holds a signing key. This supersedes the OIDC-issuer path for the console.

The `file://` anchor is **sticky across redeploys**: the deploy that injects the key records its path on the env ([`mcpauthpublickeypath`](/reference/configuration#envconfig)) as it injects it — not after the rollout, so a rollout that fails leaves the anchor named rather than nameless — and every later deploy of the runtime chart rethreads it, so a plain version bump cannot leave the edge unauthenticated. Turning the anchor off takes the explicit `erun deploy --no-mcp-auth`, and a deploy that would drop authentication the live release still has is refused with the trusted key named — see [`erun deploy` · MCP-auth stickiness](/agent-reference/cli-flags#deploy-mcp-auth).

When no trust anchor is configured the edge stays loopback-only (legacy, unauthenticated) — a desktop or hosted deploy always configures one. Capability/scope-gated authorization of *individual* tools (e.g. restricting the RCE-capable `raw` to admin-scoped tokens, while a read-only token sees only the read tools) is `(Planned.)` — it rides on the hosted role source (issue #606).

### Endpoints

:::note Shipped vs planned
The `(iss, org) → tenant` resolution model and first-identity bootstrap above are **shipped**, as are `GET /v1/whoami`, `GET /v1/tenant-issuers` (list), and `PATCH /v1/tenant-issuers` (rename a trusted issuer's display name). New tenants and their issuer mapping can be registered through the operations-only `POST /v1/tenants` below; for an existing tenant, additional issuers and their org-scoping mode are still provisioned directly in the `issuers` / `tenant_issuers` tables (migrations or the bootstrap path), not via a tenant-self-service endpoint. `POST /v1/users` enrolls additional users beyond the first-user bootstrap. A tenant-self-service **trust-management** API (a tenant adding/removing its own issuers with `audience`/`tenantClaim`/`allowedSubjects`, and the `409`/`422` codes below) is `(Planned.)`, as is the structured machine-readable error `code` field — today the API returns bare HTTP status codes with a plain-text body (see [Errors](#errors) below).
:::

| Method | Path | Description | Required scope |
|---|---|---|---|
| `GET` | `/v1/platform` | Unauthenticated self-discovery: this instance's own `issuer`, `apiUrl`, `consoleUrl`, OIDC client ids, and `brand`. Response below. | None — no bearer required |
| `GET` | `/v1/tenant-issuers` | List all issuers trusted by the caller's tenant. | Tenant member |
| `PATCH` | `/v1/tenant-issuers` | Rename a trusted issuer's display name. Body below. | Tenant admin |
| `GET` | `/v1/whoami` | Resolved identity for the calling token. Response below. | Tenant member |
| `GET` | `/v1/config` | The console's read model over the per-tenant erun config: `{tenant, environments[], contexts[]}`. | Tenant member |
| `GET` | `/v1/environments` | List the tenant's environments. | Tenant member |
| `POST` | `/v1/environments` | Register an environment in the caller's tenant, bound to a referenced context; when the deploy executor is configured, a runtime env with a pinned version also starts its durable server-side deploy (`202`). Body below. | Tenant member (write) |
| `GET` | `/v1/environments/{environment_id}` | Fetch one environment by id. | Tenant member |
| `POST` | `/v1/environments/{environment_id}/deploy` | Deploy an already-registered runtime env at a published version — the retry and version-change path (`202`). Body below. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/mcp-token` | Mint a per-env MCP bearer token (`{token, audience}`) for the caller to present to the env's `erun-mcp` edge. Body-less. Response below. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/dns01-token` | Mint a per-env DNS-01 broker token (`{token, audience}`), the credential the cluster's cert-manager DNS-01 webhook presents to the [DNS-01 broker](#dns01-broker). Body-less. Response below. | Tenant member (write) |
| `GET` | `/v1/contexts` | List the tenant's cloud contexts (managed clusters). | Tenant member |
| `POST` | `/v1/contexts` | Register a cloud context (managed cluster) and, when provisioning is configured, start its durable live bootstrap (`202`). Body below. | Tenant member (write) |
| `GET` | `/v1/contexts/{context_id}` | Fetch one cloud context by id, including its provisioning `status`. | Tenant member |
| `PUT` | `/v1/cloud-provider-aliases/{alias}` | Register/update the tenant's BYO-cloud credentials (encrypted at rest), resolved when provisioning a context. Body below. | Tenant member (write) |
| `POST` | `/v1/provision` | Return the complete, ordered **plan** to provision a hosted env (quota check → context bootstrap → namespace → env registration → runtime deploy) for the caller's tenant. Preview-only; no execution, no writes. Body below. | Tenant member (write) |
| `POST` | `/v1/tenants` | Register a new tenant plus its OIDC issuer mapping. Operations-only. Body below. | Operations only |
| `POST` | `/v1/users` | Enroll a user in the caller's tenant, or — operations-only — an explicitly named other tenant. Body below. | Tenant member (write); cross-tenant needs Operations |
| `GET` | `/v1/users` | List the caller's tenant's users, or — operations-only — an explicitly named other tenant's via `?tenantId=`. | Tenant member (read); cross-tenant needs Operations |

`GET /v1/config` is the console's read model over the per-tenant erun config — the backend DB is the system of record for the tenant's environments and cloud contexts, and this endpoint returns them denormalized as the on-disk erun config shape. All of these reads are tenant-scoped by row-level security, so a token only ever sees its own tenant's rows.

### `GET /v1/platform`

Unauthenticated — this is what a client discovers **before** it has a token: this instance's own issuer, API/console URLs, the OIDC client ids to authenticate with, and its display brand. Registered outside the auth middleware, directly on the mux next to `/healthz`. No instance's name is ever hardcoded in a client; this endpoint is how one discovers it.

```jsonc
{
  "issuer": "https://auth.erunpaas.com",
  "apiUrl": "https://api.frs-prod.services.erunpaas.com",
  "consoleUrl": "https://console.frs-prod.services.erunpaas.com",
  "consoleClientId": "12345@erun-platform",
  "cliClientId": "67890@erun-platform",
  "brand": ""
}
```

Every field is optional and independently sourced. `issuer`/`apiUrl`/`consoleUrl`/`brand` come from the env's [`platform:` block](/reference/configuration#platform-block) (threaded in at deploy via `--set-string platform.*`); an unset value renders as an empty string, **never** an error or a missing field. `consoleClientId`/`cliClientId` come from the `erun-zitadel` chart's OIDC application bootstrap (see [below](#zitadel-oidc-bootstrap)) via an optional ConfigMap — absent when that chart hasn't run, or on a platform with no hosted IdP, again rendering as `""` rather than failing the response.

A caller (an `erun cloud init <platform-api-url>`-style flow) uses this response to then fetch `<issuer>/.well-known/openid-configuration` and proceed with the Device Authorization Grant (falling back to Authorization Code + PKCE when the issuer advertises no device endpoint) against `cliClientId`. See the [erun cloud provider](#erun-cloud-provider) section below.

Errors: none — the endpoint has no input to validate and performs no authentication, so it always returns `200`.

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

### `POST /v1/environments`

Registers an **environment** in the caller's tenant. The tenant is resolved from the token — never from the body — so a token can only register an environment under its own tenant (row-level security scopes the write). The environment **runs in a referenced context**: `contextId` points at one of the tenant's cloud contexts, and the composite `(tenant_id, context_id)` foreign key enforces that the context belongs to the same tenant.

```jsonc
// POST /v1/environments body
{
  "name": "prod",              // required — a DNS-1123 label (lowercase letters, digits, internal hyphens)
  "type": "runtime",           // required — one of "runtime", "remote-agent", "local-agent"
  "contextId": "019a7fa5-…",   // optional — the cloud context (cluster) the env runs in
  "kubernetesContext": "primary", // optional — the kube context bound to the env
  "runtimeVersion": "1.2.3"    // optional — pinned runtime chart version
}
```

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
  "status": "registered",       // provisioning lifecycle: registered → provisioning → running/failed
  "deployedVersion": "1.2.2",   // omitted until a deploy has landed; see below
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

A newly-registered environment is `registered` — the row exists but nothing is deployed. The server-side deploy executor then moves it `provisioning` → `running`/`failed` and sets `provisionError` on failure; the same `status`/`provisionError` appear on `GET /v1/environments/{id}` and in the `GET /v1/config` read model.

**`runtimeVersion` vs `deployedVersion`.** `runtimeVersion` is the version the environment is **pinned** to — operator-authored, set at registration. `deployedVersion` is the version a deploy **actually installed**, written when that deploy reaches `running`. They are equal in the steady state and diverge in exactly two cases: a [`POST /v1/environments/{id}/deploy`](#deploy-endpoint) that named a different version, and a failed deploy — which leaves `deployedVersion` on the version the cluster is still running, because a deploy that failed did not remove what was already there. `deployedVersion` is omitted until the environment's first successful deploy.

**Per-tenant environment-count quota.** After validating the body and before persisting, the endpoint enforces the tenant's environment-count cap: it compares how many environments the tenant already has against the cap and rejects the registration with HTTP `409` once the tenant is at or over it. The cap defaults to **10** and is overridden per tenant by a `tenant_quotas.max_environments` row. That override row is set by the operations-only [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) endpoint (below). Both the count and the cap are read under row-level security, so each is scoped to the caller's own tenant.

**Server-side deploy executor.** When configured, the backend deploys the runtime chart itself: it runs the deploy as a Kubernetes `Job` in the tenant's `<tenant>-devops` runtime image (which carries `erun` + `helm` + `kubectl`) under a cluster-admin ServiceAccount, invoking `erun deploy <tenant> <env> --version <runtimeVersion>`, and watches the Job to completion — succeeded → `running`, failed → `failed` with the reason on `provisionError`. A durable workflow (DBOS) wraps this, keyed by environment id, so a control-plane restart resumes an in-flight deploy rather than double-deploying. The deploy image is `<registry>/<tenant>-devops:<runtimeVersion>` (image-baked config model).

**What `provisionError` carries on a failed deploy.** The failure happens inside the Job, so the executor reads it back before the Job's TTL reaps the pod and records it verbatim under a `deploy job failed for version <version>:` prefix. Three sources, in order — the first that yields anything wins:

1. **The deploy's own output** — the tail of the Job pod's log (up to 40 non-empty lines, 4000 characters, keeping the *end*, since `erun deploy` prints its failure last). This is the actionable case: a version whose runtime chart was never published surfaces the deploy's `MISSING_CHART_IN_REGISTRY` error in full, naming the version and every `<registry>/charts/<chart>` coordinate the [runtime chart search](/agent-reference/cli-flags#deploy-runtime-chart-search) probed, plus the three ways out that page specs. This is the whole point of reading the Job's output back: the erun platform chart is published only beside the runtime image ERun releases, so an environment whose deploy registry holds nothing but its own application images has no chart there at any version — a failure the version alone never explains.
2. **The pod's status** — for a deploy that never logged a line, e.g. `deploy pod <name>: container deploy is waiting: ImagePullBackOff: <message>` when the cluster cannot pull `<registry>/<tenant>-devops:<version>`.
3. **The Job's terminal condition** — `deploy job <name> failed: DeadlineExceeded: …` when the pod is already gone (the Job's `activeDeadlineSeconds` is 30 minutes).

When none of the three yields anything, `provisionError` says so and names the `kubectl -n <platform-namespace> logs job/<job-name>` an Operator can run while a deploy is in flight. Reading the reason needs `pods` + `pods/log` `get`/`list`/`watch` in the platform namespace, which the `api.envDeployer.enabled` chart value already grants the API's ServiceAccount.

The executor is **opt-in and off by default** — it requires the backend to run in-cluster with a durable-workflow database, a kube client, and the deployer ServiceAccount configured (chart value `api.envDeployer.enabled`, which also provisions the SA and its cluster-admin binding). When it is off, or the env is not a `runtime` env, or no `runtimeVersion` is pinned, the endpoint is register-only (`201`) exactly as before.

Registration deploys an environment **once**. To deploy it again — retrying a failure, or moving it to another published version — use [`POST /v1/environments/{id}/deploy`](#deploy-endpoint).

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name` is not a DNS-1123 label (the env forms the `<tenant>-<env>` namespace), `type` is not one of `runtime`/`remote-agent`/`local-agent`, or the body is not valid JSON. | Send a DNS-1123 `name` and a valid `type`. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/environments`. | Send a valid token whose roles permit the write. |
| `409` | The tenant is at its environment-count cap (default `10` unless a `tenant_quotas` row overrides it); the body is `environment quota reached: this tenant already has N of N environments`. | Delete an unused environment, or raise the tenant's cap via [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) (operations-only). |
| `500` | Persistence failed — e.g. `contextId` references a context that is not the caller's (the composite `(tenant_id, context_id)` foreign key is violated), or the request-scoped security context is missing (an internal wiring error). The row is persisted **before** the deploy is started, so a `failed to start provisioning` `500` (the deploy executor could not enqueue the durable workflow) leaves the environment registered — re-create is a no-op conflict; poll `GET /v1/environments/{id}` to confirm the row exists. | Reference a context owned by the caller's tenant; if it persists with a valid context, it is a server bug. |

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

**Error behaviour.** Bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | The environment is not a `runtime` env (`only a runtime environment can be deployed`); no version resolves — the body omitted one and the environment has no pinned `runtimeVersion` (`version is required: …`); or the body is present but not valid JSON (`invalid request body`). | Deploy a runtime env, and name a published `version` when the environment has no pin. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers this write. | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (row-level security returns not-found for another tenant's env, never leaking its existence). | Deploy an environment id the caller's tenant owns. |
| `409` | A deploy is already in flight for this environment (`a deploy is already in progress for this environment`); the claim is held. | Poll `GET /v1/environments/{id}` until it leaves `provisioning`, or wait out the 45-minute stale window if the holder crashed. |
| `501` | The deploy executor is not configured (`the deploy executor is not configured`) — the backend has no durable-workflow database, kube client, or deployer ServiceAccount. | Enable `api.envDeployer.enabled` on the backend chart. |
| `500` | The claim write failed, or the durable workflow could not be enqueued (`failed to start deploy`). The claim is taken **before** the workflow starts, so this leaves the environment at `provisioning` with nothing running; it becomes re-deployable once the stale window elapses. | Retry after the stale window; if it persists, it is a server bug. |

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

Provide **either** a `context` block (provision a new cluster — its bootstrap plan is the real `InitCloudContext` dry-run argv) **or** a `kubernetesContext` (reuse an existing context). When a `context` block is present it wins and `kubernetesContext` is ignored.

**The ordered `plan`.** The response is `{ "plan": [ … ], "quotaOk": <bool> }`. `plan` is the human-readable, audit-style ordered list of every action the live provision would take, in this exact order:

1. **authz/tenant** — `provision: tenant <tenant> (resolved from token)`.
2. **quota** — `quota: tenant has <count> of <cap> environments` followed by ` — within quota` or ` — WOULD EXCEED, provisioning blocked`. The cap is the tenant's `tenant_quotas.max_environments` (default `10`); both reads are row-level-security-scoped to the caller's tenant.
3. **context** — when a `context` block was given: a `context: bootstrap cluster <name> via alias <alias>` header line followed by the full `InitCloudContext` dry-run argv (the security-group + IAM instance-profile setup, `ec2 run-instances`, the k3s install user-data, the kube-context wiring), exactly the plan [`POST /v1/contexts`](#post-v1contexts) returns. Otherwise: `context: reuse existing kubernetes context <kubernetesContext>`.
4. **namespace** — `namespace: would create <tenant>-<env>`.
5. **register** — `register: would persist environment <name> (<type>) in tenant <tenant> referencing context <ref>`.
6. **deploy** — `deploy: would helm install the erun-devops runtime chart (release <tenant>-devops) into <tenant>-<env>`.

**`quotaOk`.** `true` when the provision fits under the tenant's environment-count cap, `false` when it would exceed it. When `quotaOk` is `false` the endpoint **still returns the full plan** with HTTP `200` (it is a preview, not a write), and the quota line names the block — surfacing the blocking decision the way a dry-run does, rather than rejecting with a `409`. A caller gating on the quota should check `quotaOk`, not the status code.

```jsonc
// 200 response (new cluster, within quota)
{
  "plan": [
    "provision: tenant acme (resolved from token)",
    "quota: tenant has 2 of 10 environments — within quota",
    "context: bootstrap cluster acme-prod via alias acme-aws",
    "aws ec2 create-security-group …",
    "aws ec2 run-instances …",
    "kubectl config set-context acme-prod …",
    "namespace: would create acme-prod",
    "register: would persist environment prod (runtime) in tenant acme referencing context acme-prod",
    "deploy: would helm install the erun-devops runtime chart (release acme-devops) into acme-prod"
  ],
  "quotaOk": true
}
```

**Live orchestration is `(Planned.)`.** This endpoint only resolves and returns the plan. The live orchestration — `InitCloudContext` with `DryRun=false` → ensure the namespace → `RunBootstrapInitWithDependencies` / deploy the runtime chart → wire the env exposure and the per-env auth edge — requires a live AWS account and cluster and is **not executed** in this build. It composes the same dry-run primitives as `POST /v1/contexts` plus the registration endpoints, so the live path is the composition of their live paths.

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `environment.name` is not a DNS-1123 label, `environment.type` is not one of `runtime`/`remote-agent`/`local-agent`, a `context` block is present but missing `name`/`cloudProviderAlias`/`region`, or the body is not valid JSON. | Send a valid `environment` and, if provisioning a new cluster, a complete `context` block. |
| `400` | The context bootstrap plan could not be resolved (e.g. an unsupported `region`, `instanceType`, or `diskSizeGb`). | Use a supported region/instance type/disk size. |
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

### `POST /v1/users` and `GET /v1/users`

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

Omitting `issuer`/`subject` enrolls a username with **no external identity yet** — the row exists, but no token can resolve to it until one is linked, and there is no separate endpoint to link one after the fact in this build. Enrollment grants the same predefined `ReadAll`/`WriteAll` roles every bootstrapped user gets — there is no finer-grained role-assignment surface yet.

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

### `PUT /v1/tenants/{tenant_id}/quota` {#put-v1tenantstenant_idquota}

Sets a tenant's **environment-count cap** — the per-tenant override the [`POST /v1/environments`](#post-v1environments) quota guardrail enforces. **Operations-only**, like tenant registration: the caller's resolved tenant must be `OPERATIONS`, because it writes another tenant's `tenant_quotas` row (the operations role's RLS policy permits cross-tenant writes; the row's `tenant_id` is set explicitly to the path's `{tenant_id}`, not the operations caller's own tenant). The write upserts, so it both creates and updates the cap.

```jsonc
// PUT /v1/tenants/019a7fa5-…/quota body
{ "maxEnvironments": 50 }   // required — the cap (>= 0); 0 blocks all new environments

// 200 response
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f50",
  "maxEnvironments": 50,
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:05:00Z"
}
```

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `tenant_id` is empty, `maxEnvironments` is negative, or the body is not valid JSON. | Send a non-negative `maxEnvironments`. |
| `403` | The caller's resolved tenant is not an `OPERATIONS` tenant (the explicit operations gate). | Call from an operations-tenant token. |

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
