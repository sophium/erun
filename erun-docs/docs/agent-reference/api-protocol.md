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

### Per-env MCP edge authentication {#mcp-edge}

The per-environment `erun-mcp` server is exposed publicly (Traefik routes it at `mcp.<tenant>-<env>.services.<base-domain>`) and its `raw` tool can `kubectl exec`, so it is RCE-sensitive and **must always be authenticated** once a trust anchor is configured. The edge resolves the tenant from the verified token's issuer — the same `(iss) → tenant` model as the REST API, applied per URL.

The runtime chart configures each edge with a set of trusted issuers mapping each issuer to the tenant it authenticates (`ERUN_MCP_TRUSTED_ISSUERS`, a JSON `{"<issuer>":"<tenant>"}` object; `ERUN_MCP_TRUSTED_ISSUER` + `ERUN_TENANT` is single-issuer sugar). A request is authorized when:

1. `Authorization: Bearer <jwt>` is present — missing → `401`.
2. The token's `iss` is a trusted issuer for this edge — untrusted → `401`; the mapped value is the resolved tenant.
3. The signature verifies against that issuer's key, and `exp` and the audience (`aud`) match. **Unlike the REST API, the MCP edge enforces `aud`** — the per-env audience (`erun-mcp:<tenant>/<environment>`) means a token minted for one environment cannot be replayed against another, or against the REST API.
4. The resolved tenant matches **this** environment's tenant (a per-env edge serves exactly one tenant) — a token resolving to another tenant → `401`. Tenant-scoped tools are likewise pinned to the edge's own environment: a `tenant`/`environment` argument that differs from the pod's identity is refused, so a caller can never drive one env's MCP to act on another (issue #657).

An edge can trust **multiple issuers at once**, of two kinds, dispatched by the *configured* issuer's scheme (not the token's claimed `iss`, so the verification path can't be attacker-chosen):

- **`https://` OIDC issuer** (the platform's Zitadel, AWS) — verified via the issuer's JWKS through provider discovery, using the same shared verifier the REST API uses (so the API and every MCP edge verify identically). Hosted/console callers present their OIDC token directly.
- **`file://` desktop key** (issue #655) — a self-contained local trust anchor for the **desktop** case, instead of an OIDC IdP: the desktop generates an Ed25519 key (`desktopid.key`) once, signs an EdDSA JWT whose `iss` is a `file://<path>` URL naming the public key, and injects that public key into the runtime pod on deploy (`erun deploy --mcp-auth-public-key`). The edge loads the key from that `file://` path and verifies the signature against it; `alg` is hard-locked to `EdDSA` for `file://` issuers, closing the alg-confusion class.

When no trust anchor is configured the edge stays loopback-only (legacy, unauthenticated) — a desktop or hosted deploy always configures one. Capability/scope-gated authorization of *individual* tools (e.g. restricting the RCE-capable `raw` to admin-scoped tokens, while a read-only token sees only the read tools) is `(Planned.)` — it rides on the hosted role source (issue #606).

### Endpoints

:::note Shipped vs planned
The `(iss, org) → tenant` resolution model and first-identity bootstrap above are **shipped**, as are `GET /v1/whoami`, `GET /v1/tenant-issuers` (list), and `PATCH /v1/tenant-issuers` (rename a trusted issuer's display name). New tenants and their issuer mapping can be registered through the operations-only `POST /v1/tenants` below; for an existing tenant, additional issuers and their org-scoping mode are still provisioned directly in the `issuers` / `tenant_issuers` tables (migrations or the bootstrap path), not via a tenant-self-service endpoint. A tenant-self-service **trust-management** API (a tenant adding/removing its own issuers with `audience`/`tenantClaim`/`allowedSubjects`, and the `409`/`422` codes below) is `(Planned.)`, as is the structured machine-readable error `code` field — today the API returns bare HTTP status codes with a plain-text body (see [Errors](#errors) below).
:::

| Method | Path | Description | Required scope |
|---|---|---|---|
| `GET` | `/v1/tenant-issuers` | List all issuers trusted by the caller's tenant. | Tenant member |
| `PATCH` | `/v1/tenant-issuers` | Rename a trusted issuer's display name. Body below. | Tenant admin |
| `GET` | `/v1/whoami` | Resolved identity for the calling token. Response below. | Tenant member |
| `GET` | `/v1/config` | The console's read model over the per-tenant erun config: `{tenant, environments[], contexts[]}`. | Tenant member |
| `GET` | `/v1/environments` | List the tenant's environments. | Tenant member |
| `POST` | `/v1/environments` | Register an environment in the caller's tenant, bound to a referenced context. Body below. | Tenant member (write) |
| `GET` | `/v1/environments/{environment_id}` | Fetch one environment by id. | Tenant member |
| `GET` | `/v1/contexts` | List the tenant's cloud contexts (managed clusters). | Tenant member |
| `POST` | `/v1/contexts` | Register a cloud context (managed cluster) for the caller's tenant and return its cluster-bootstrap plan. Body below. | Tenant member (write) |
| `GET` | `/v1/contexts/{context_id}` | Fetch one cloud context by id. | Tenant member |
| `POST` | `/v1/tenants` | Register a new tenant plus its OIDC issuer mapping. Operations-only. Body below. | Operations only |

`GET /v1/config` is the console's read model over the per-tenant erun config — the backend DB is the system of record for the tenant's environments and cloud contexts, and this endpoint returns them denormalized as the on-disk erun config shape. All of these reads are tenant-scoped by row-level security, so a token only ever sees its own tenant's rows.

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

On success the endpoint persists the row and returns it with HTTP `201`:

```jsonc
// 201 response
{
  "environmentId": "019a7fa5-c2c0-7c55-bc70-714873a71f30",
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "name": "prod",
  "type": "runtime",
  "kubernetesContext": "primary",
  "contextId": "019a7fa5-c2c0-7c55-bc70-714873a71f20",
  "runtimeVersion": "1.2.3",
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

**Per-tenant environment-count quota.** After validating the body and before persisting, the endpoint enforces the tenant's environment-count cap: it compares how many environments the tenant already has against the cap and rejects the registration with HTTP `409` once the tenant is at or over it. The cap defaults to **10** and is overridden per tenant by a `tenant_quotas.max_environments` row. That override row is set **out-of-band** today — directly in the `tenant_quotas` table (a migration or operator-run statement); an operations quota-set endpoint is `(Planned.)`. Both the count and the cap are read under row-level security, so each is scoped to the caller's own tenant.

**Live namespace + runtime-chart deploy is `(Planned.)`.** This endpoint registers the environment row only; it does **not** yet ensure the `<tenant>-<env>` namespace or deploy the runtime chart server-side (that runs `RunBootstrapInitWithDependencies` against a live cluster). Until that lands, a registered environment stands as registered config — the namespace creation and chart deploy from the registered row are the planned follow-up.

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name` is not a DNS-1123 label (the env forms the `<tenant>-<env>` namespace), `type` is not one of `runtime`/`remote-agent`/`local-agent`, or the body is not valid JSON. | Send a DNS-1123 `name` and a valid `type`. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/environments`. | Send a valid token whose roles permit the write. |
| `409` | The tenant is at its environment-count cap (default `10` unless a `tenant_quotas` row overrides it); the body is `environment quota reached: this tenant already has N of N environments`. | Delete an unused environment, or raise the tenant's `tenant_quotas.max_environments` cap (out-of-band today; an operations quota-set endpoint is `(Planned.)`). |
| `500` | Persistence failed — e.g. `contextId` references a context that is not the caller's (the composite `(tenant_id, context_id)` foreign key is violated), or the request-scoped security context is missing (an internal wiring error). | Reference a context owned by the caller's tenant; if it persists with a valid context, it is a server bug. |

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
- **`preview: false`** (default) → registers a context row at a **pending** status (no instance id / public IP yet) and returns `{ "context": { … }, "plan": [ … ] }` with HTTP `201`.

```jsonc
// 201 response (preview=false)
{
  "context": {
    "contextId": "019a7fa5-c2c0-7c55-bc70-714873a71f20",
    "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
    "name": "primary",
    "provider": "aws",
    "cloudProviderAlias": "aws-acme",
    "region": "eu-west-2",
    "instanceType": "c8gd.2xlarge",
    "diskType": "gp3",
    "diskSizeGb": 100,
    "kubernetesContext": "primary",
    "createdAt": "2026-06-24T10:00:00Z",
    "updatedAt": "2026-06-24T10:00:00Z"
  },
  "plan": [
    "cloud-context init: alias=aws-acme region=eu-west-2 instance-type=c8gd.2xlarge disk=100GB/gp3",
    "aws ec2 create-security-group …",
    "aws ec2 run-instances …",
    "kubectl config set-cluster primary …"
  ]
}
```

The k3s admin token is a **server secret** and is never part of the response or the context read model.

**Live bootstrap execution and token custody are `(Planned.)`.** This endpoint registers the row and returns the plan; it does **not** yet execute the real (non-dry-run) EC2 + k3s bootstrap or take server-side custody of the k3s admin token / kubeconfig. Until that lands, a registered context stays at its pending status with no instance id or public IP — provisioning the live cluster from the plan is the planned follow-up, gated on the hosted platform having a live AWS path.

**Error behaviour.** Today the API returns a bare HTTP status with a plain-text body (no JSON envelope):

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name`, `cloudProviderAlias`, or `region` is empty/missing, or the body is not valid JSON. | Send all three required fields. |
| `400` | The bootstrap plan could not be resolved (e.g. an unsupported `region`, `instanceType`, or `diskSizeGb` for the BYO-cloud bootstrap). | Use a supported region/instance type/disk size. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/contexts`. | Send a valid token whose roles permit the write. |
| `500` | Persistence failed (e.g. missing request-scoped security context — an internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

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
