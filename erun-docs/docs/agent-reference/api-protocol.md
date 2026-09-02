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

  **A client of an org-scoped issuer must request the scope that puts this claim on the token, or it cannot resolve to any tenant.** For the erun-shipped Zitadel, that claim is `urn:zitadel:iam:user:resourceowner:id`, minted only when the client requests the `urn:zitadel:iam:user:resourceowner` OAuth scope. The CLI/desktop OIDC login requests it by default (falling back to a plain login, once, if the issuer refuses the scope — the common shape for a dedicated/BYO issuer that has never heard of it), and the console does the same. A client of your own that talks to a shared issuer directly needs to request this scope itself; omitting it produces `401 TENANT_UNRESOLVED` (see [Errors](#errors)) for every caller on that issuer, even one who is already enrolled in a tenant there.
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
3. (OIDC path) Fetch (or read from cache) the issuer's `<iss>/.well-known/openid-configuration` and its `jwks_uri` JWKS, and verify the JWT signature and registered claims (`exp`, `nbf`, `iat`). Failure → `401`. Then apply the **audience allow-list** below. (The `file://` desktop path enforces its own fixed `erun-api` audience instead, so a token minted for an MCP env — audience `erun-mcp:<tenant>/<env>` — cannot be replayed against the API. That check is independent of the allow-list and unaffected by it.)
4. Look up `issuers.org_field_key` for `iss`.
   - If `iss` is **not registered**: unauthorized (`401`) — **unless** the `tenants` table is empty, which triggers first-identity bootstrap (below).
   - If `org_field_key` is **NULL**: resolve `tenant_issuers` where `issuer = iss` and `org_field_value IS NULL` → tenant.
   - If `org_field_key` is **set**: read that claim's value (`org`) from the token. Empty/absent → `401`. Otherwise resolve `tenant_issuers` where `issuer = iss` and `org_field_value = org` → tenant.
5. Resolve the ERun user from `(tenant, iss, sub)` via `user_external_ids`. Unknown subject → `401` — **except** when the resolved tenant has **no users yet**, in which case the first valid token for it is enrolled as that tenant's first user with both `ReadAll` and `WriteAll` (per-tenant first-user bootstrap, below). Once a tenant has any user, unknown subjects for it stay unauthorized.
6. Authorize the request against the user's roles/permissions; on success, allow and write the audit event.

#### The OIDC audience allow-list {#oidc-audience-allow-list}

The issuer allow-list in step 2 answers "which IdP minted this?", not "which of that IdP's clients is calling?". A hosted IdP puts the registered **client id** in `aud`, so every client of a trusted issuer clears the issuer check — the `aud` claim is what separates them. The API applies that policy as a configured allow-list:

| Setting | Where |
|---|---|
| `--oidc-allowed-audiences <csv>` | `eapi` flag |
| `ERUN_OIDC_ALLOWED_AUDIENCES` | env var; the API image's entrypoint translates it into the flag |
| `api.oidcAllowedAudiences` | `erun-backend-api` chart value; **empty by default** |

Resolution:

1. **Empty (the default)** → no audience check on the OIDC path. Any audience an allowed issuer minted is accepted, matching the issuer allow-list's own empty-means-any rule.
2. **Non-empty** → the token's `aud` must contain at least one listed value. An OIDC `aud` may list several audiences; intersecting on any one of them passes.
3. **A token with no `aud` claim** is rejected whenever the allow-list is non-empty — an explicit statement of which audiences may call cannot be satisfied by a token that names none.

Which state is in force is reported on the API's own startup line, so it is readable from the logs rather than inferred:

```
oidc audience enforcement=off (any audience from an allowed issuer is accepted)
oidc audience enforcement=on (allowed: console-client, cli-client)
```

**Error behaviour**

| Failure mode | What happens | Recovery |
|---|---|---|
| Allow-list set; token's `aud` matches none of it | `401`; the rejection names the audiences the token carried and the ones configured (never any other token content) | Add the client id to `api.oidcAllowedAudiences`, or call with a token minted for a listed client |
| Allow-list set; token carries no `aud` | `401` naming the expected audiences | Configure the IdP client to mint an `aud`, or clear the allow-list |
| Allow-list unset | Request proceeds; no audience check | Set `api.oidcAllowedAudiences` to turn the boundary on |

Turning it on is a **configuration change**, not a code change: the client ids a given deployment's IdP mints are a fact about that deployment, so a wrong list refuses every caller at once. Read the ids off the IdP first (for an erun-shipped Zitadel, the console and CLI client ids the `erun-zitadel` bootstrap publishes — the same values [`GET /v1/platform`](#platform-endpoint) serves as `consoleClientId`/`cliClientId`), confirm them against a real token's `aud`, then set the value.

**First-identity bootstrap.** When the `tenants` table is empty, the first valid token bootstraps the system: it creates an `OPERATIONS` tenant, registers its `iss` in `issuers`, creates the first user, and grants it both `ReadAll` and `WriteAll`.

**What "RLS-scoped" means for an `OPERATIONS` caller.** Every tenant-owned table (as opposed to a root resolution table like `tenants`/`tenant_issuers`, which carries no row-level security at all — see [`GET /v1/tenants`](#get-v1tenants)) carries two PostgreSQL row-level security policies: `erun_tenant` (an ordinary `COMPANY` caller's role) is scoped to `tenant_id = erun_current_tenant_id()`, but `erun_operations` (an `OPERATIONS` caller's role) is `USING (true)` — unconditional, by design, so an operations caller's queries can reach any tenant's rows when a capability genuinely needs that reach. This means "read under row-level security" is **not** the same statement for the two roles: a query against a tenant-owned table with no explicit `tenant_id` predicate returns one tenant's rows under `erun_tenant` and **every** tenant's rows under `erun_operations`. An endpoint whose contract is "the caller's own tenant's count/list" — an environment-count quota, a usage total — must therefore add that predicate itself, reading the tenant id off the security context, rather than describing the read as simply "RLS-scoped" and trusting RLS alone to supply the caller's own tenant for every role.

**The bootstrap tenant's name, and why it can drift.** The `OPERATIONS` tenant is named after this instance's own declared identity — its `ERUN_TENANT` config — so that hosted provisioning's first resolve of `<tenant>-devops` (see [Provisioning lifecycle](/concepts/hosted-platform#provisioning-lifecycle)) finds an image the platform actually publishes, rather than a placeholder name nobody will ever publish under. `ERUN_TENANT` unset at that moment falls back to the literal name `operations`. **This naming decision is made exactly once, against an empty `tenants` table** — bootstrap never re-runs once any tenant exists, so a platform whose `ERUN_TENANT` was unset (or different) the very first time it ever started keeps that original name indefinitely, even after `ERUN_TENANT` is later corrected. Nothing before this tolerated that silently: the API's startup log now reports the disagreement plainly (`tenant name mismatch: declared tenant is "<ERUN_TENANT>", OPERATIONS tenant is "<actual name>"`) whenever the two disagree, instead of leaving it discoverable only by querying `tenants` directly. [`PATCH /v1/tenants/reconcile-bootstrap-name`](#patch-v1tenantsreconcile-bootstrap-name) below is the one-way repair for a platform in that state.

How that issuer is registered decides whether the platform can ever host a second tenant on it, so it is not a fixed choice: when the bootstrap token carries an org claim an erun-shipped IdP is known to emit (today `urn:zitadel:iam:user:resourceowner:id`, which needs the token minted with the `urn:zitadel:iam:user:resourceowner` scope), the issuer is registered **org-scoped** on that claim and the bootstrap tenant's mapping records its own org value. Any other issuer keeps the single-tenant registration (`org_field_key` NULL), which is the right shape for a dedicated per-tenant IdP. Registering a *shared* IdP single-tenant is a one-way door — org-scoping mode lives on the shared `issuers` row, so every later tenant on that issuer is refused; see [`PATCH /v1/tenant-issuers`](#patch-v1tenant-issuers) for the conversion that recovers a platform already in that state.

**Per-tenant first-user bootstrap.** Bootstrap is not limited to the empty-`tenants` case. Whenever a token resolves to a tenant (a registered issuer, the right org claim for org-scoped issuers) that has **zero** users, the first such valid token is enrolled as that tenant's first user with `ReadAll` + `WriteAll` — this is how a newly-provisioned tenant gets its first admin without a separate user-management call. **For an org-scoped issuer this means the first valid caller in a freshly-provisioned org becomes that tenant's admin**, so provisioning a tenant + registering its issuer/org is the act that authorizes its first caller. After a tenant has at least one user, unknown subjects for it — and unknown/unregistered issuers anywhere — stay unauthorized.

**Audit.** Each authorized API request records `external_issuer_id` (the `iss`), `external_org_id` (the org claim value for org-scoped issuers; null for single-tenant), `external_user_id` (the `sub`), and the resolved `erun_user_id` — see [the audit log spec](/agent-reference/audit-log).

### Per-env MCP edge authentication {#mcp-edge}

The per-environment `erun-mcp` server is exposed publicly (Traefik routes it at `mcp.<tenant>-<env>.services.<base-domain>`) and its `raw` tool can `kubectl exec`, so it is RCE-sensitive and **must always be authenticated** once a trust anchor is configured. The edge resolves the tenant from the verified token's issuer — the same `(iss) → tenant` model as the REST API, applied per URL.

The runtime chart configures each edge with a set of trusted issuers mapping each issuer to the tenant it authenticates (`ERUN_MCP_TRUSTED_ISSUERS`, a JSON `{"<issuer>":"<tenant>"}` object; `ERUN_MCP_TRUSTED_ISSUER` + `ERUN_TENANT` is single-issuer sugar). A request is authorized when:

1. `Authorization: Bearer <jwt>` is present — missing → `401`.
2. The token's `iss` is a trusted issuer for this edge — untrusted → `401`; the mapped value is the resolved tenant.
3. The signature verifies against that issuer's key, and `exp` and the audience (`aud`) match — the per-env audience (`erun-mcp:<tenant>/<environment>`) means a token minted for one environment cannot be replayed against another, or against the REST API (whose `file://` path enforces its own `erun-api` audience — issue #674). The REST API's OIDC path applies a configured allow-list instead of a fixed audience, since the audiences a hosted IdP mints are per-deployment — see [the audience allow-list](#oidc-audience-allow-list) above.
4. The resolved tenant matches **this** environment's tenant (a per-env edge serves exactly one tenant) — a token resolving to another tenant → `401`. Tenant-scoped tools are likewise pinned to the edge's own environment: a `tenant`/`environment` argument that differs from the pod's identity is refused, so a caller can never drive one env's MCP to act on another (issue #657).

An edge can trust **multiple issuers at once**, of two kinds, dispatched by the *configured* issuer's scheme (not the token's claimed `iss`, so the verification path can't be attacker-chosen):

- **`https://` OIDC issuer** — the chart's `mcpAuth.issuer`/`mcpAuth.enabled` values support pointing the edge at an OIDC issuer's JWKS instead of a local key, verified through the same shared verifier the REST API uses. Nothing in erun currently writes this: `erun deploy` never resolves an OIDC issuer for an env, so the only way a running edge ends up on this path is a hand-set chart value. Every deploy-driven env — desktop or hosted — uses the `file://` key path below.
- **`file://` key** (issues #655, #686) — a self-contained trust anchor instead of an OIDC IdP: an Ed25519 public key injected into the runtime pod when the env's runtime is deployed (`erun deploy --mcp-auth-public-key <key>`, or `erun init --mcp-auth-public-key <key>`, which folds it into init's create-time deploy so the desktop needs no separate post-init redeploy). The signer stamps a `file://<path>` `iss` naming that public key; the edge loads the key from that path and verifies the EdDSA signature, with `alg` hard-locked to `EdDSA` (closing the alg-confusion class). Two signers use it, and each env picks exactly one:
  - **Desktop** — the desktop generates the key (`desktopid.key`) once and signs each token locally.
  - **Hosted (console)** — the **backend** is the signer, the hosted twin of the desktop: it holds the MCP signing key (`ERUN_API_MCP_SIGNING_KEY_PATH`) and mints a per-env token on the console's behalf via [`POST /v1/environments/{id}/mcp-token`](#mcp-token-endpoint). The env's server-side deploy Job injects the backend's own public key automatically (no Operator action, no `--mcp-auth-public-key`) — so the console never holds a signing key and every hosted env's edge is authenticated by default once the backend has a signing key configured (issue #1084).

The `file://` anchor is **sticky across redeploys**: the deploy that injects the key records its path on the env ([`mcpauthpublickeypath`](/reference/configuration#envconfig)) as it injects it — not after the rollout, so a rollout that fails leaves the anchor named rather than nameless — and every later deploy of the runtime chart rethreads it, so a plain version bump cannot leave the edge unauthenticated. Turning the anchor off takes the explicit `erun deploy --no-mcp-auth`, and a deploy that would drop authentication the live release still has is refused with the trusted key named — see [`erun deploy` · MCP-auth stickiness](/agent-reference/cli-flags#deploy-mcp-auth).

When no trust anchor is configured the edge stays loopback-only (legacy, unauthenticated) — a desktop or hosted deploy always configures one.

**Capability tiers gate individual tools.** A verified token's `scope` claim (space-delimited) resolves to one or more of three tiers: `erun:read` (observation only — `version`, `list`, `idle`, `doctor`-style reads, `ai_sessions`, `activity_lease_list`, `context_list`, `cloud_list`, `exec_diff`, `observe`, `usage`, `outputs_list`/`outputs_download`, `exec_job_status`/`exec_job_output`/`exec_job_await`, `review_list`/`review_show`/`review_queue_list`), `erun:admin` (every tool, including the RCE-capable `raw` and every mutation), and `erun:attach` (drives the [WebSocket attach edge](#mcp-attach-endpoint) and nothing else — it grants neither read nor admin, and neither of those grants it back). A token carrying no `scope` claim resolves to `erun:admin` — the desktop's own single-operator compatibility default, since a token minted before capabilities existed must keep working. A tool not in the read-only list defaults to requiring `erun:admin`: the table is an allow-list, so a newly added tool is unreachable to a read-only caller until someone decides otherwise. Any tier can be combined (e.g. `erun:read erun:attach`), and `erun:admin` alone already satisfies every tier.

**Minting a scoped token is real; per-caller entitlement to a tier is not (issue #1877).** [`POST /v1/environments/{id}/mcp-token`](#mcp-token-endpoint) lets a caller request any of the three tiers, validated only against the fixed vocabulary above — not against whether *this* caller should be trusted with it. Any tenant member (the mint route's `TenantUserClass`, not just `TenantAdmin`) can today request `erun:admin` for any environment their tenant owns. Treat this as the same trust boundary a hosted console operator already sits inside (they can already reach the environment through other admin-tier routes), not as a control that narrows what an already-authenticated tenant member can do — a caller building an unattended or autonomous consumer of a minted token (rather than a human clicking "mint" once per session) should not assume a request for a narrower tier is enforced against who is asking.

### Endpoints

:::note Shipped vs planned
The `(iss, org) → tenant` resolution model and first-identity bootstrap above are **shipped**, as are `GET /v1/whoami`, `GET /v1/tenant-issuers` (list), `PATCH /v1/tenant-issuers` (rename a trusted issuer's display name, or convert a single-tenant issuer to org-scoped), and [`PATCH /v1/tenants/reconcile-bootstrap-name`](#patch-v1tenantsreconcile-bootstrap-name) (the platform's own one-way legacy-name repair). New tenants and their issuer mapping can be registered through the operations-only `POST /v1/tenants` below; for an existing tenant, additional issuers and their org-scoping mode are still provisioned directly in the `issuers` / `tenant_issuers` tables (migrations or the bootstrap path), not via a tenant-self-service endpoint. `POST /v1/users` enrolls additional users beyond the first-user bootstrap. A tenant-self-service **trust-management** API (a tenant adding/removing its own issuers with `audience`/`tenantClaim`/`allowedSubjects`, and the `409`/`422` codes below) is `(Planned.)`, as is the deeper JWT-verification-level structured `code` catalogue (`UNSUPPORTED_ALG`, `INVALID_SIGNATURE`, etc.) further down. The tenant/user **resolution** outcome does carry a machine-readable `{code, message}` envelope today — `TENANT_UNRESOLVED` vs `NOT_ENROLLED` vs `RESOLUTION_FAILED` vs `UNAUTHENTICATED` (see [Errors](#errors) below) — since collapsing those into one generic message told already-enrolled callers to ask for an enrolment that already existed, or blamed enrolment for what was really an internal error. Business-logic errors past the auth layer do carry a `code` today too — see [Reviews · Errors](/collaboration/reviews#errors).
:::

| Method | Path | Description | Required scope |
|---|---|---|---|
| `GET` | `/v1/platform` | Unauthenticated self-discovery a caller resolves **before** signing in: this instance's own `issuer`, `apiUrl`, `consoleUrl`, OIDC client ids, and white-label surface (`brand`, `docsUrl`, `tagline`, `logoUrl`). Response below. | None — no bearer required |
| `GET` | `/v1/tenant-issuers` | List the caller's tenant's trusted issuers, or — operations-only — an explicitly named other tenant's via `?tenantId=`. | Tenant member (read); cross-tenant needs Operations |
| `PATCH` | `/v1/tenant-issuers` | Rename a trusted issuer's display name. Body below. | Tenant admin |
| `GET` | `/v1/whoami` | Resolved identity for the calling token. Response below. | Tenant member |
| `GET` | `/v1/config` | The console's read model over the per-tenant erun config: `{tenant, environments[], contexts[]}`. | Tenant member |
| `GET` | `/v1/environments` | List the caller's tenant's environments, or — operations-only — an explicitly named other tenant's via `?tenantId=`. | Tenant member (read); cross-tenant needs Operations |
| `POST` | `/v1/environments` | Register an environment in the caller's tenant, bound to a referenced context; when the deploy executor is configured, a runtime env with a pinned version also starts its durable server-side deploy (`202`). `tenantId` in the body targets another tenant — operations-only, audited (see below). Body below. | Tenant member (write); cross-tenant needs Operations |
| `GET` | `/v1/environments/{environment_id}` | Fetch one environment by id. | Tenant member |
| `POST` | `/v1/environments/{environment_id}/deploy` | Deploy an already-registered runtime env at a published version — the retry and version-change path (`202`). Body below. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/stop` | Scale a runtime env's Deployment to zero — the server-side equivalent of `erun stop`. Does not change the env's provisioning `status`. Body-less. | Tenant member (write) |
| `DELETE` | `/v1/environments/{environment_id}` | Start tearing down a runtime env's namespace (skipped if it never deployed) and removing its row — the server-side equivalent of `erun delete`. Asynchronous: `202 Accepted` with the row at `status: deleting`; poll to see it converge. Not recoverable. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/mcp-token` | Mint a per-env MCP bearer token (`{token, audience, scope}`) for the caller to present to the env's `erun-mcp` edge. Optional body `{scope}` requests a capability tier; minting `erun:admin` additionally requires the entitlement below. Response below. | Tenant member (write); `erun:admin` additionally requires the delete-environment entitlement |
| `POST` | `/v1/environments/{environment_id}/dns01-token` | Mint a per-env DNS-01 broker token (`{token, audience}`), the credential the cluster's cert-manager DNS-01 webhook presents to the [DNS-01 broker](#dns01-broker). Body-less. Response below. | Tenant member (write) |
| `PUT` | `/v1/environments/{environment_id}/hostname` | Point the caller's own environment's wildcard hostname at an IP by performing the platform's own PowerDNS write — for a caller with no direct PowerDNS access to the platform cluster (a developer's local cluster, most concretely). Body `{targetIp}`. Response below. | Tenant member (write) |
| `DELETE` | `/v1/environments/{environment_id}/hostname` | Remove the caller's own environment's wildcard hostname record, symmetric with the `PUT` above. Body-less; `204` on success. | Tenant member (write) |
| `POST` | `/v1/environments/{environment_id}/ai-sessions` | The environment's own AI-tool hooks report their turn-boundary status (busy/idle/awaiting-input/exited) for one session. Body below. | Tenant member (write) |
| `GET` | `/v1/environments/{environment_id}/ai-sessions` | Read back the resolved status of every session last reported for this environment. Response below. | Tenant member (read) |
| `GET` | `/v1/contexts` | List the tenant's cloud contexts (managed clusters). | Tenant member |
| `POST` | `/v1/contexts` | Register a cloud context (managed cluster) and, when provisioning is configured, start its durable live bootstrap (`202`). Body below. | Tenant member (write) |
| `GET` | `/v1/contexts/{context_id}` | Fetch one cloud context by id, including its provisioning `status`. | Tenant member |
| `PUT` | `/v1/cloud-provider-aliases/{alias}` | Register/update the tenant's BYO-cloud credentials (encrypted at rest), resolved when provisioning a context. Body below. | Tenant member (write) |
| `POST` | `/v1/provision` | Return the complete, ordered **plan** to provision a hosted env (quota check → placement → context bootstrap → namespace → env registration → runtime deploy → auth-edge wiring → exposure) for the caller's tenant. Preview-only; no execution, no writes. Body below. | Tenant member (write) |
| `POST` | `/v1/tenants` | Register a new tenant plus its OIDC issuer mapping. Operations-only. Body below. | Operations only |
| `GET` | `/v1/tenants` | List every tenant (operations-only caller), or a single-item list containing just the caller's own tenant otherwise. | Tenant member (read) |
| `GET` | `/v1/tenants/reachable` | List the tenants the caller's own verified identity maps to — the one endpoint that deliberately answers across tenant scope. Response below. | Tenant member (read) |
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
  "mobileClientId": "",
  "brand": "Acme",
  "docsUrl": "https://docs.acme.erunpaas.com",
  "tagline": "Ship it, prove it.",
  "logoUrl": "https://acme.erunpaas.com/logo.svg",
  "version": "1.0.221"
}
```

Every field is optional and independently sourced. `issuer`/`apiUrl`/`consoleUrl`/`brand`/`docsUrl`/`tagline`/`logoUrl` come from the env's [`platform:` block](/reference/configuration#platform-block) (threaded in at deploy via `--set-string platform.*`); an unset value renders as an empty string, **never** an error or a missing field. `consoleClientId`/`cliClientId`/`mobileClientId` come from the `erun-zitadel` chart's OIDC application bootstrap (see [below](#zitadel-oidc-bootstrap)) via an optional ConfigMap — absent when that chart hasn't run, or on a platform with no hosted IdP, again rendering as `""` rather than failing the response. `mobileClientId` is also `""` on a platform that runs `erun-zitadel` but has never configured `zitadel.oidc.mobileRedirectUris`: unlike the console and CLI apps, no `erun-mobile` OIDC application is minted until an operator names the redirect URI a real mobile client will use, since that custom URL scheme belongs to whichever client ships and this platform has no default to guess.

**`version` is the build actually serving this response — not a release tag, and not the calling client's own version.** A tag can exist before any deployment serves it, and an API build that predates a new request field silently ignores it rather than rejecting it, so this is the only non-destructive way to answer "is fix X actually deployed here" (see the [release flow](/deployment/release-flow) for why a tag and a live deployment can disagree). It is stamped into the `eapi` binary via `-ldflags` at image build time — the same `ERUN_VERSION` build arg and mechanism `erun`/`emcp` use for `erun version` — so it always names the image that is actually running, never a value baked into the chart or read from an env var. Deliberately **not** an empty string when unresolved: unlike the other fields above, an empty `version` would misread as "no build happened" rather than "this binary was never stamped with one," so an `eapi` built outside the release pipeline (a bare `go build`, a one-off local image) reports the literal string `"dev"` instead. A real deployment produced by `erun build`/`erun release` always reports a real version, since every image build threads its resolved version into this same build arg. No commit hash or build host is exposed here — a version string is normal discovery-document practice, but this endpoint is unauthenticated and world-readable, so nothing that narrows an attacker's picture of the deployment's internals is added beyond the version already published in the image tag.

`docsUrl` defaults to `https://docs.<basedomain>` when the platform block sets a base domain, so an instance links its own documentation with nothing configured. `tagline` and `logoUrl` have no default — empty is what keeps the client's bundled product text and generic mark in place. `logoUrl` is deliberately an **absolute URL**, not a path this API serves: one built console image serves every instance and carries no brand asset, so the logo lives wherever the operator hosts it.

**How the console uses it.** On load, before rendering the sign-in prompt, the console fetches this endpoint and drives its OIDC Authorization Code + PKCE flow from `issuer` + `consoleClientId` (see [Sign-in](#sign-in-oidc) for the flow itself; `src/auth/auth.ts` is the implementation). A console built against an **older API with no `/v1/platform`** gets a `404`, and against a **newer API with the fields left unset** gets `200` with empty strings — both fall back to its own build-time `VITE_OIDC_ISSUER`/`VITE_OIDC_CLIENT_ID` (a local-dev override only), rather than failing to render. `brand`, `docsUrl`, `tagline`, and `logoUrl` are what the signed-out landing page renders — the document title, the docs link, the `<h1>` pitch, and the header mark respectively — each falling back to a bundled product default when empty, so a half-configured instance renders a coherent page rather than a blank hero. A `logoUrl` the browser cannot load falls back to the same generic mark as an unset one, so a moved or blocked asset never leaves a broken image on the front door. `apiUrl`/`consoleUrl`/`cliClientId`/`version` are carried for other clients (a CLI `erun login` flow, or an Agent/operator comparing the serving build against a release); the console does not consume them yet.

**How `erun cloud` uses it.** A caller (an `erun cloud init <platform-api-url>`-style flow) uses this response to then fetch `<issuer>/.well-known/openid-configuration` and proceed with the Device Authorization Grant (falling back to Authorization Code + PKCE when the issuer advertises no device endpoint) against `cliClientId`. See the [erun cloud provider](#erun-cloud-provider) section below.

**Error behaviour.** No input to validate and no authentication performed, so a server that implements this endpoint always returns `200`. A `404` instead means an older API predates the endpoint entirely — the recovery is the client-side fallback described above, not an operator action.

#### Zitadel OIDC application bootstrap {#zitadel-oidc-bootstrap}

The `erun-zitadel` chart provisions the OIDC applications `consoleClientId`/`cliClientId`/`mobileClientId` above resolve to, idempotently, via a sidecar in the same pod as Zitadel core (it needs the shared bootstrap volume to read the org-owner PAT core writes):

- **`erun-console`** — a `OIDC_APP_TYPE_USER_AGENT` (SPA) app, Authorization Code + PKCE, redirect/post-logout URI derived from the env's `platform.consoleUrl`.
- **`erun-cli`** — a `OIDC_APP_TYPE_NATIVE` (public) app supporting both the Device Authorization Grant (`OIDC_GRANT_TYPE_DEVICE_CODE`) and Authorization Code + PKCE with loopback redirect URIs (`http://127.0.0.1/callback`, `http://localhost/callback`).
- **`erun-mobile`** — a `OIDC_APP_TYPE_NATIVE` (public) app supporting Authorization Code + PKCE only (a mobile client always has a system browser to redirect through, so it needs no device-code fallback), redirect URI(s) from `zitadel.oidc.mobileRedirectUris`. Unlike the other two apps this one has no default: the sidecar mints no `erun-mobile` application at all while `mobileRedirectUris` is unset, the same "skip, don't guess" behavior `erun-console` falls back to when `platform.consoleUrl` is unset.

All three are configured with `accessTokenType: OIDC_TOKEN_TYPE_JWT` — load-bearing, since erun's bearer verifier validates a JWT via OIDC discovery + JWKS and rejects Zitadel's default opaque access token with `401 invalid bearer token`. The sidecar publishes the resulting client ids to a `<tenant>-zitadel-oidc-clients` ConfigMap in the release namespace, which the `erun-backend-api` chart reads via an optional `configMapKeyRef` (`optional: true` — absent ConfigMap, absent env var, empty string in the `/v1/platform` response above).

Every OIDC application shares one issuer, so `ERUN_OIDC_ALLOWED_AUDIENCES`/`api.oidcAllowedAudiences` (the [OIDC audience allow-list](#oidc-audience-allow-list) above) is what scopes which of `consoleClientId`/`cliClientId`/`mobileClientId` may actually call this API, if an operator chooses to turn audience enforcement on at all — minting a fourth client here does not by itself change that policy or its default (unset, accepting any audience an allowed issuer minted).

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

### `GET /v1/tenant-issuers` {#get-v1tenant-issuers}

Lists the caller's tenant's trusted issuer mappings — `tenantId`, `issuer`, `name`, and, for an org-scoped issuer, `orgFieldKey`/`orgFieldValue` (each mapping's own org value; see [Identity model](#tenant-issuers)). An operations-scoped caller may instead read another tenant's mappings via `?tenantId=<tenant_id>`, the same `resolveTargetTenant` convention [`GET /v1/users`](#post-v1users-and-get-v1users) and [`GET /v1/invites`](#invites) use: omitted or equal to the caller's own tenant reads the caller's own mappings; a different value is refused with `403` for any caller whose tenant is not `OPERATIONS`.

This is how the console resolves a target tenant's org before [enrolling an identity into it](/agent-reference/identity-administration#enrolling-into-another-organization) — the operator names a tenant, and the console reads that tenant's `orgFieldValue` here rather than asking for a raw Zitadel org id.

### `PATCH /v1/tenant-issuers`

Renames a trusted issuer's display `name` for the caller's tenant, **or** converts a single-tenant issuer to an org-scoped one.

```jsonc
// PATCH /v1/tenant-issuers body — rename
{
  "issuer": "https://issuer.example.com/oauth2/default",
  "name": "Acme corporate SSO"
}
```

Returns the updated tenant-issuer record (`200`). `400` if `issuer` or `name` is empty; `404` if the `(tenant, issuer)` pair is not trusted by the caller's tenant.

**Converting to org-scoped.** Sending `orgFieldKey` + `orgFieldValue` instead switches the issuer's resolution mode and backfills the caller's own mapping in one transaction:

```jsonc
// PATCH /v1/tenant-issuers body — convert a shared IdP to org-scoped
{
  "issuer": "https://auth.example.com",
  "orgFieldKey": "urn:zitadel:iam:user:resourceowner:id",  // the claim that selects a tenant
  "orgFieldValue": "386994597030592700"                    // this mapping's own org
}
```

This exists because an issuer registered single-tenant cannot otherwise be widened: org-scoping mode is a property of the shared `issuers` row, so the first tenant on a shared IdP would otherwise foreclose every later one with no way back short of editing the database.

Both fields are required **together** — either alone leaves resolution broken, since a key with no value orphans the issuer's existing tenant and a value with no key is read by nothing. Unlike a rename, which touches only the caller's own mapping, this rewrites the shared registry row and therefore changes how every tenant's tokens on that issuer resolve, so it carries the same **operations-only** gate `POST /v1/tenants` applies to these root resolution tables.

`400` if only one of the two is sent; `403` if the caller's tenant is not `OPERATIONS`; `404` if the `(tenant, issuer)` pair is not trusted by the caller's tenant. After converting, every token on that issuer must carry the named claim — a token minted without it resolves nothing, so confirm the client requests whatever scope the IdP needs (for Zitadel, `urn:zitadel:iam:user:resourceowner`; `erun cloud login --scope` requests it).

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

Registers an **environment** in the caller's tenant by default. The tenant is resolved from the token — never trusted from the body as the default case — so an ordinary token can only register an environment under its own tenant (row-level security scopes the write). The environment **runs in a referenced context**: `contextId` points at one of the tenant's cloud contexts, and the composite `(tenant_id, context_id)` foreign key enforces that the context belongs to the same tenant.

**Cross-tenant administration (#1816).** For the Operator view, see [Administering another tenant](/collaboration/cross-tenant-administration). An explicit `tenantId` in the body targets a **different** tenant, honored only when the caller's resolved tenant is `OPERATIONS` — the same cross-tenant precedent as [`POST /v1/users`](#post-v1users-and-get-v1users) and [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota): a non-operations caller naming another tenant is refused with `403` before any write, and naming the caller's own tenant explicitly is a no-op, not an error. `GET /v1/environments` takes the same target, as a `?tenantId=` query param, for the read half. Every quota and placement check below (environment-count cap, resource-cap floor, aggregate budget, cluster placement) runs against the **target** tenant, not the operator's own — an operations caller administering another tenant is bound by that tenant's quota, exactly as that tenant's own caller would be. A cross-tenant create is recorded in a second, explicit audit event (beyond the ordinary per-request one every API call gets) naming the target tenant, the operator, and the operator's own home tenant, so the write is attributable even though the row itself, once persisted, only ever names the target.

```jsonc
// POST /v1/environments body
{
  "name": "prod",              // required — a DNS-1123 label (lowercase letters, digits, internal hyphens)
  "type": "runtime",           // required — one of "runtime", "remote-agent", "local-agent"
  "contextId": "019a7fa5-…",   // optional — see "Placement" below
  "kubernetesContext": "primary", // optional, remote-agent/local-agent only — see "Placement" below
  "runtimeVersion": "1.2.3",   // optional — pinned runtime chart version
  "preview": false,            // optional — resolve and return the POST /v1/provision plan instead of creating the row
  "tenantId": "019a…"          // optional — operations-only cross-tenant target (#1816)
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
  "exposedHostname": "mcp.acme-prod.services.erunpaas.com", // omitted until the environment is actually exposed; see below
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

A newly-registered environment is `registered` — the row exists but nothing is deployed. The server-side deploy executor then moves it `provisioning` → `running`/`failed` and sets `provisionError` on failure. A requested teardown moves it to `deleting`, from which it either disappears entirely (the row is hard-deleted once the namespace is confirmed gone) or lands on `deletion-blocked` with `deleteError` naming why — see [`DELETE /v1/environments/{environment_id}`](#delete-endpoint). `running` never survives a delete attempt. `status`, `provisionError`, `exposeError`, `exposedHostname`, and `deleteError` all appear identically on `GET /v1/environments/{id}` and in the `GET /v1/config` read model.

**`runtimeVersion` vs `deployedVersion`.** `runtimeVersion` is the version the environment is **pinned** to — operator-authored, set at registration. `deployedVersion` is the version a deploy **actually installed**, written when that deploy reaches `running`. They are equal in the steady state and diverge in exactly two cases: a [`POST /v1/environments/{id}/deploy`](#deploy-endpoint) that named a different version, and a failed deploy — which leaves `deployedVersion` on the version the cluster is still running, because a deploy that failed did not remove what was already there. `deployedVersion` is omitted until the environment's first successful deploy.

**`exposedHostname` (#1902).** The environment's MCP edge hostname (`mcp.<tenant>-<env>.<services-zone>`), so a client can discover the edge it is entitled to reach instead of an Operator hand-pasting one. It is **omitted**, never present-but-empty, when the environment is not exposed — whether that means the chained expose was never attempted (this platform has no ingress IP configured) or every attempt so far has failed (see `exposeError` for why); the API never distinguishes "not exposed" from "exposed, hostname unknown" because that second state cannot occur. The value is computed, not read back from the cluster or the DNS record: it is the same deterministic formula `erun expose` itself resolves from the tenant, environment, and the platform's own configured services zone, recorded only once the deploy Job's chained `erun expose mcp` step has actually reported success for this environment. Once set it is preserved across later writes that are not themselves a successful re-expose (a failed redeploy, a synchronous precondition failure) — the cluster is still serving the last hostname that succeeded, so an unrelated failure must not make discovery of it disappear. No route is capability-gated beyond what `GET /v1/environments/{id}` already requires (tenant member, read) — learning where an already-readable environment's edge lives needs no more privilege than reading the environment itself, the same reasoning `GET /v1/environments/{environment_id}/ai-sessions` uses.

**Per-tenant environment-count quota.** After validating the body and before persisting, the endpoint enforces the tenant's environment-count cap: it compares how many environments the tenant already has against the cap and rejects the registration with HTTP `409` once the tenant is at or over it. The cap defaults to **10** and is overridden per tenant by a `tenant_quotas.max_environments` row. That override row is set by the operations-only [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) endpoint (below). Both the count and the cap are scoped explicitly to the tenant the write targets — the caller's own by default, or the `tenantId` named above for an operations caller — read off the security context rather than left to row-level security alone — the same operations-caller distinction the note under [First-identity bootstrap](#sign-in-oidc) above explains. **Environments mid-teardown do not count.** The comparison excludes rows at `deleting` and `deletion-blocked`: the delete that would free the slot is the same call that is stuck, so counting a wedged teardown would lock a tenant out of its own allowance. The aggregate resource budget below counts differently — it uses the tenant's runtime-environment count as-is, mid-teardown rows included.

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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name` is not a DNS-1123 label (the env forms the `<tenant>-<env>` namespace), `type` is not one of `runtime`/`remote-agent`/`local-agent`, the body is not valid JSON, a `runtime` environment set a raw `kubernetesContext` (no known credential — see [Placement](/concepts/hosted-platform#single-cluster-placement) above), or a `runtime` environment's `contextId` does not resolve for the caller's tenant. | Send a DNS-1123 `name` and a valid `type`; reference a `contextId` you registered, or leave both unset for the platform's own cluster. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/environments`. | Send a valid token whose roles permit the write. |
| `403` | `tenantId` names a tenant other than the caller's own, and the caller's resolved tenant is not `OPERATIONS` (see "Cross-tenant administration" above). | Omit `tenantId` to act on your own tenant, or use a token whose resolved tenant is `OPERATIONS`. |
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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

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

The body is optional:

```jsonc
// request body (optional)
{
  "scope": "erun:admin" // space-delimited; one or more of erun:read, erun:admin, erun:attach
}
```

An absent or empty `scope` mints `erun:read` — the least capability a token can carry and still be useful, never the desktop's admin-by-default compatibility case (that default is for a token carrying no `scope` claim *at all*, which this route never produces). A `scope` naming anything outside the fixed vocabulary is rejected outright (`400`) rather than silently dropped or widened — see [Capability tiers](#mcp-edge) above for what each tier grants. On success, HTTP `200`:

```jsonc
// 200 response
{
  "token": "<eddsa-jwt>",
  "audience": "erun-mcp:acme/prod",
  "scope": "erun:admin"
}
```

**Requesting `erun:admin` requires an entitlement beyond reaching this route.** Every tenant member can reach this endpoint and mint `erun:read`/`erun:attach` for any environment their tenant owns — the same reach the endpoint's own permission class already grants for operating an environment that already exists. `erun:admin` is not a peer of that class: it also lets the holder delete, terraform, and initialize the environment through its MCP edge, actions no ordinary tenant member may take through the API. Requesting it therefore additionally requires the same permission that would let the caller call [`DELETE /v1/environments/{environment_id}`](#delete-endpoint) — in practice, `TenantAdmin` or a platform operator. A caller without it is refused `403` and never receives a token for that request; the same caller can still mint `erun:read`/`erun:attach` in a separate request.

**Backend signing key.** The signer is enabled by pointing `ERUN_API_MCP_SIGNING_KEY_PATH` at the backend's Ed25519 private key (PKCS#8 PEM) — on a hosted deploy, the `erun-backend-api` chart's `api.mcpSigning.secretName` value mounts that key Secret and sets the path (opt-in; unset leaves the endpoint at `501`). The matching public key is what a deploy injects into the env (`erun deploy --mcp-auth-public-key`), so the edge trusts backend-signed tokens.

**Usable once the env is deployed.** A minted token only authenticates against a **deployed** env whose edge already carries the backend's public key. A dedicated `409`-until-deployed guard is `(Planned.)` — the backend tracks a per-env provisioning `status` (see [`POST /v1/environments`](#post-v1environments)) but the mint endpoint does not yet gate on it reaching `running`; until it does, the endpoint mints whenever the signer is configured and the environment exists.

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `400` | The body is present but not valid JSON, or `scope` names something other than `erun:read`/`erun:attach`/`erun:admin`. | Send a body with a recognized `scope`, or no body at all. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)); any tenant member reaches the route itself. | Send a valid token for a member of the environment's tenant. |
| `403` | `scope: erun:admin` was requested but the caller does not hold the delete-environment entitlement (see above). | Request `erun:read`/`erun:attach` instead, or have a `TenantAdmin`/operator mint the admin-scoped token. |
| `404` | No environment with `{environment_id}` in the caller's tenant (row-level security returns not-found for another tenant's env, never leaking its existence). | Mint for an environment id the caller's tenant owns. |
| `501` | No backend MCP signing key is configured (`ERUN_API_MCP_SIGNING_KEY_PATH` unset). | Configure the signing key on the backend, or use the desktop `file://` path. |
| `500` | The tenant read or the signing failed (e.g. missing request-scoped security context — an internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

### `GET <mcpPath>/attach/{session}` (WebSocket) {#mcp-attach-endpoint}

Not a REST endpoint and not JSON-RPC — a second HTTP surface on the same per-env `erun-mcp` edge (`mcp.<tenant>-<env>.services.<base-domain>`), upgraded to a WebSocket. It bridges the caller directly to a live `dtach` session already running in the environment's own pod (the same session `erun open --ai` or a linked desktop orchestrator attaches to), one binary/text frame at a time. See [erun-mcp/AGENTS.md's "The WebSocket Attach Edge Is Not An MCP Tool"](https://github.com/sophium/erun/blob/main/erun-mcp/AGENTS.md) for the implementation rationale; this section specs the wire contract a caller needs.

**Authentication — two channels, same token.** A caller that can set arbitrary HTTP headers (a CLI, a mobile app) authenticates exactly like the JSON-RPC path: `Authorization: Bearer <jwt>`, requiring the `erun:attach` capability (see [Capability tiers](#mcp-edge) above). A **browser** cannot set that header on a WebSocket handshake at all — its `WebSocket` constructor exposes only the subprotocol list — so a browser caller instead offers `new WebSocket(url, ["erun.bearer.v1", "<jwt>"])`, joined by the handshake into one `Sec-WebSocket-Protocol` header. The edge tries the `Authorization` header first, then this fallback; a successful handshake echoes back `Sec-WebSocket-Protocol: erun.bearer.v1` (never the token) per RFC 6455. Either channel is checked for the `erun:attach` capability **before** the WebSocket upgrade — a caller without it gets a plain HTTP `403` and no session is touched.

**Wire protocol** once the socket is open:

| Direction | Frame type | Payload |
|---|---|---|
| Client → server | Binary | Raw bytes written to the PTY (keystrokes). |
| Client → server | Text | `{"type":"resize","cols":<int>,"rows":<int>}` — resizes the far end's PTY. |
| Server → client | Binary | Raw PTY output bytes. |
| Server → client | Text | `{"type":"outcome","outcome":"<value>"}` — sent **exactly once**, immediately before the server closes the socket. |

`outcome` is one of `taken-over` (a second attach to the same session id evicted this one — the session itself keeps running), `deploy-reattach` (the session ended because the environment's runtime redeployed), `ended` (the underlying process exited on its own), or `unknown` (the process could not be reaped, e.g. a signal kill — never guessed as `ended`). A client that observes the socket close with **no** prior `outcome` frame (a network drop) should treat the result as `unknown` itself — the server's own guarantee is only that it sends the frame before *its* close, not that every close carries one.

**Error behaviour:**

| Status | Condition | Recovery |
|---|---|---|
| `401` | No bearer token resolved from either channel, or the token fails verification (see [Per-env MCP edge authentication](#mcp-edge)). | Send a valid token via the header or the subprotocol offer. |
| `403` | The resolved capability set does not include `erun:attach` (an `erun:read`-only or unscoped-but-narrower token). | Mint a token whose scope includes `erun:attach`. |
| `400` | The `{session}` path segment is empty. | Name the session id to attach to. |

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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). `WriteAll` covers this write. | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (RLS returns not-found for another tenant's env). | Mint for an environment id the caller's tenant owns. |
| `501` | No backend signing key is configured (`ERUN_API_MCP_SIGNING_KEY_PATH` unset). | Configure the signing key on the backend. |
| `500` | The tenant read or signing failed (internal wiring error). | Retry; if it persists, it is a server bug. |

### `PUT` / `DELETE` `/v1/environments/{environment_id}/hostname` {#environment-hostname-endpoint}

Lets a tenant point its own environment's wildcard hostname at an IP through the platform API instead of the direct `pdnsutil` exec `erun expose` otherwise uses — the write path a caller with no credentials for the platform's own cluster needs (a developer's local cluster, most concretely; see [Networking spec · Platform service exposure](/agent-reference/networking-spec#platform-service-exposure)). The environment is resolved from `{environment_id}` under row-level security, so a caller can only ever write `*.<its tenant>-<its env>.<servicesZone>` — never another tenant's or another environment's record.

```jsonc
// PUT /v1/environments/{environment_id}/hostname body
{
  "targetIp": "127.0.0.1"    // required; any valid IP -- a private or loopback address is accepted on purpose
}
```

```jsonc
// 200 response
{
  "hostname": "*.acme-prod.services.erunpaas.com",
  "targetIp": "127.0.0.1"
}
```

`DELETE` takes no body and returns `204` on success, removing the same wildcard record.

Performs the write itself against the same PowerDNS the [DNS-01 broker](#dns01-broker) writes ACME challenges to, over RFC2136 DNS UPDATE (TSIG-signed), not the direct `kubectl exec ... pdnsutil` `erun expose` runs when it has cluster access — cluster access to the platform never has to leave the platform. `erun expose`/`erun unexpose` call this route automatically once an `erun`-type cloud alias is configured locally (`erun cloud init erun`) and no `--services-zone`/`--platform-namespace` override is given (that override is the hosted deploy Job's own signal that it already has direct PowerDNS access); `--erun-alias` disambiguates when more than one alias is configured. Enabled by the same PowerDNS write path (nameserver, zone, TSIG key/secret) the DNS-01 broker uses; unconfigured → `501`.

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)):

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (RLS returns not-found for another tenant's env). | Write against an environment id the caller's tenant owns. |
| `400` | `targetIp` is missing or not a valid IP address. | Pass a real IP; a private or loopback one is fine. |
| `501` | No PowerDNS write path is configured on the backend. | Configure the DNS-01 broker's PowerDNS settings on the backend. |
| `500` | The DNS write itself failed, or the tenant/environment read failed (internal wiring error). | Retry; if it persists, it is a server bug or a PowerDNS outage. |

### `POST /v1/environments/{environment_id}/ai-sessions` {#ai-sessions-endpoint}

Lets an environment's own AI-tool hooks report a turn-boundary event for one session (`ai`, `contribute-ai`, `open-<slot>`) — the authenticated-edge twin of the structured busy/idle/awaiting-input status model the desktop and per-env MCP already resolve locally from the same events, replacing what used to be a PTY output-volume heuristic. A later report **replaces** the previous one outright for that session: only the most recent event decides the resolved state, never how long it has been since. The environment is resolved from `{environment_id}` under row-level security, so a caller can only report against its own tenant's environment.

```jsonc
// POST /v1/environments/{environment_id}/ai-sessions body
{
  "sessionId": "ai",             // required
  "tool": "claude",              // optional — an event that omits it carries the previously reported tool forward
  "event": "turn-end",           // required — one of: turn-start, tool-use, turn-end, notify, exit
  "exitCode": null,              // optional — set on an "exit" event
  "exitReason": ""               // optional — set on an "exit" event; the literal value "oom" resolves state to oom-killed
}
```

There is no client-supplied timestamp: the backend stamps its own receipt time, so a caller cannot make a stale or clock-skewed report read as current.

**Resolved state.** On success (`201`) the endpoint returns the same resolved shape a local caller sees:

```jsonc
// 201 response
{
  "sessionId": "ai",
  "tool": "claude",
  "state": "awaiting-input",     // idle | busy | awaiting-input | exited | oom-killed
  "reason": "finished its turn and is waiting for your next message",
  "lastActivity": "2026-08-31T21:53:01Z",
  "exitCode": null
}
```

`event` → `state` resolution: `turn-start`/`tool-use` → `busy`; `turn-end`/`notify` → `awaiting-input`; `exit` with `exitReason: "oom"` → `oom-killed`; any other `exit` → `exited`. A session with no recorded event at all reads as `idle`, never as an error.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). | Send a valid token whose roles permit the write. |
| `404` | No environment with `{environment_id}` in the caller's tenant (RLS returns not-found for another tenant's env, never leaking its existence). | Report against an environment id the caller's tenant owns. |
| `400` | `sessionId` is empty, or `event` is not one of the five recognized values. | Fix the request body; an unrecognized event is refused rather than silently resolving to `idle`. |
| `500` | The write failed (internal wiring error or repository failure). | Retry; if it persists, it is a server bug. |

### `GET /v1/environments/{environment_id}/ai-sessions` {#ai-sessions-read-endpoint}

Reads back the resolved status of every session the environment above has ever reported, sorted by session id — the read-back half of the self-report above, for a caller (erun-console, and eventually a native companion client) with no local kubeconfig or port-forward to poll instead. Each entry is the same resolved shape the `POST` above returns on success:

```jsonc
// 200 response
[
  {
    "sessionId": "ai",
    "tool": "claude",
    "state": "awaiting-input",
    "reason": "finished its turn and is waiting for your next message",
    "lastActivity": "2026-08-31T21:53:01Z",
    "exitCode": null
  }
]
```

An environment with no reported sessions reads as `[]`, never `null` — a caller ranging over the body needs no null check.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `401` / `403` | Standard auth failures (see [Errors](#errors)). | Send a valid token whose roles permit the read. |
| `404` | No environment with `{environment_id}` in the caller's tenant (RLS returns not-found for another tenant's env, never leaking its existence). | Read against an environment id the caller's tenant owns. |
| `500` | The read failed (internal wiring error or repository failure). | Retry; if it persists, it is a server bug. |

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

**Client-side login.** The Basic password this endpoint expects is not a distinct "registry token" a caller has to go mint separately — it is the same `erun-api` OIDC access token every other authenticated call on this page uses, the one [`CloudProviderBearerToken`](#erun-cloud-provider) already resolves for an `erun`-type cloud provider alias. `erun build`/`erun push`/`erun release` obtain it automatically for a `registry.erunpaas.com` push: `DockerRegistryLoginWithHostedRegistry` (`erun-common/build_docker_commands.go`) intercepts a login for that host specifically, resolves the operator's cloud provider alias via `ResolveERunPlatformAlias` (the caller's sole configured `erun`-type alias — zero or more than one is refused with a clear error, not a guess), mints a fresh bearer token from it, and runs `docker login registry.erunpaas.com -u erun --password-stdin` with the token piped over stdin — never argv, so it never appears in a process listing. Every other registry host falls through to the pre-existing login path (the GHCR-aware fallback, then a plain interactive `docker login`) unchanged. A caller with no configured `erun`-type alias gets that refusal instead of a `docker login` prompt waiting on a password nobody can type; running `erun cloud login <alias>` first (after `erun cloud init erun --api-url <url>`) is what supplies it.

**Error behaviour.** This endpoint lives in its own `internal/registrytoken` package, outside `internal/routes` — bare HTTP status with a plain-text body, not the `{code, message}` envelope [Reviews · Errors](/collaboration/reviews#errors) documents for the rest of this API:

| Status | Condition | Recovery |
|---|---|---|
| `401` | Missing/malformed Basic credentials, or the password does not verify as a valid, unexpired, correctly-audienced (`erun-api`) bearer token from a trusted issuer. | `erun build`/`erun push` resolve this automatically (see **Client-side login** above); by hand, send `docker login registry.erunpaas.com` (or an equivalent Basic-auth client) with a current tenant API token as the password. |
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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `environment.name` is not a DNS-1123 label, `environment.type` is not one of `runtime`/`remote-agent`/`local-agent`, a `context` block is present but missing `name`/`cloudProviderAlias`/`region`, or the body is not valid JSON. | Send a valid `environment` and, if provisioning a new cluster, a complete `context` block. |
| `400` | The context bootstrap plan could not be resolved (e.g. an unsupported `region`, `instanceType`, or `diskSizeGb`). | Use a supported region/instance type/disk size. |
| `400` | A `runtime` environment named a `context` block or `kubernetesContext` (see [Placement](/concepts/hosted-platform#single-cluster-placement) above). | Leave both unset for a `runtime` environment. |
| `401` / `403` | Standard auth failures (see [Errors](#errors)). The `WriteAll` permission covers `POST /v1/provision`. | Send a valid token whose roles permit the write. |
| `500` | The tenant or quota read failed (e.g. missing request-scoped security context — an internal wiring error, never a client fault). | Retry; if it persists, it is a server bug. |

Note that being **at or over quota is not an error** here — it returns `200` with `quotaOk: false` and the full plan (see `quotaOk` above), unlike `POST /v1/environments`, which rejects the actual write with `409`.

### `GET /v1/tenants` {#get-v1tenants}

Lists tenants. An **operations-tenant** caller sees **every** tenant this platform hosts; any other caller sees a single-item list containing only their own resolved tenant — `tenants` is a root resolution table with no RLS, so this scoping happens in application code rather than the database.

```jsonc
// 200 response (operations-tenant caller)
[
  {
    "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f50",
    "name": "acme",
    "type": "COMPANY",
    "createdAt": "2026-06-24T10:00:00Z",
    "updatedAt": "2026-06-24T10:00:00Z",
    "userCount": 3,
    "resolvable": true
  },
  {
    "tenantId": "019a8012-...-000",
    "name": "validationagent",
    "type": "COMPANY",
    "createdAt": "2026-08-30T09:00:00Z",
    "updatedAt": "2026-08-30T09:00:00Z",
    "userCount": 0,
    "resolvable": true
  },
  {
    "tenantId": "019a8b21-...-000",
    "name": "probeco",
    "type": "COMPANY",
    "createdAt": "2026-08-30T09:00:00Z",
    "updatedAt": "2026-08-30T09:00:00Z",
    "userCount": 0,
    "resolvable": false
  }
]
```

`resolvable` answers "can *anybody* sign in to this tenant" — whether at least one of its `tenant_issuers` rows is a mapping the [resolution algorithm](#tenant-issuers) can ever match. A mapping is resolvable when the issuer's org-scoping mode and the mapping's org value agree: an org-scoped issuer needs an `org_field_value` (resolution matches it by equality, and an empty org claim is rejected before it gets that far), and a single-tenant issuer must not have one (resolution matches `NULL` there). `probeco` above is the mismatch: registered on an org-scoped issuer with no org value, so it exists, lists, accepts enrollments, and **no token can ever resolve to it**.

Like `userCount`, `resolvable` is populated **only** by the operations-tenant branch; every other tenant-returning shape omits the field. Absent means "this read did not compute it", never "it works" — do not coalesce a missing `resolvable` to `true`. `POST /v1/tenants` refuses to create an unresolvable mapping (below), so a `false` here is a tenant registered before that refusal existed, or one whose issuer was converted after the fact; repair it by giving the mapping the org value its issuer resolves by ([`PATCH /v1/tenant-issuers`](#patch-v1tenant-issuers)).

`userCount` is populated **only** for the operations-tenant branch above, in a single query (a `LEFT JOIN`/`GROUP BY` over `users`, not one query per tenant) — a tenant with genuinely zero users reports the explicit number `0`, never `null` and never an omitted field. Every other tenant-returning shape in this API (this endpoint's own single-tenant branch, `POST /v1/tenants`'s create response, `GET /v1/tenants/reachable`, `PATCH /v1/tenants/reconcile-bootstrap-name`) never computes it and omits the field entirely. A caller must treat "field absent" and "field present with value `0`" as different facts — the first means "not counted here", the second means "counted, and it is zero" — never coalesce a missing `userCount` to `0`.

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)); no endpoint-specific codes.

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

**`orgFieldKey` only ever registers a *new* issuer.** The org-scoping mode lives once on the shared `issuers` row, so an issuer already in the registry keeps the mode it has and the `orgFieldKey` in this body is ignored. That is why the endpoint validates `orgFieldValue` against the registry's **effective** mode rather than the requested one, and refuses a mapping the effective mode can never match (`UNRESOLVABLE_ISSUER_MAPPING`, below) instead of writing a tenant nobody can sign in to. Read the effective mode first with [`GET /v1/tenant-issuers`](#get-v1tenant-issuers) when in doubt.

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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `name` is empty or contains anything other than lowercase letters and digits (no hyphens — so the `<tenant>-<env>` namespace stays injective), `issuer` is empty/missing, `type` is not one of `COMPANY`/`OPERATIONS`, or the body is not valid JSON. | Send a hyphen-free lowercase-alphanumeric `name`, a non-empty `issuer`, and a valid `type`. |
| `403` | The caller's resolved tenant is not an `OPERATIONS` tenant (the explicit operations gate, beyond the standard auth failures in [Errors](#errors)). | Call from an operations-tenant token whose roles permit the write. |
| `409` | The `(issuer, org_field_value)` mapping already exists — either the issuer is already registered single-tenant (no org discriminator) and `orgFieldValue` was left empty, or this exact org value on that issuer is already taken; the body names which. | For the no-discriminator case, an operations caller converts the issuer to org-scoped via [`PATCH /v1/tenant-issuers`](#patch-v1tenant-issuers) (which backfills the existing tenant's mapping) before retrying this call with `orgFieldKey`/`orgFieldValue`; for the taken-org-value case, pick a different `orgFieldValue`. |
| `409` `UNRESOLVABLE_ISSUER_MAPPING` | The requested mapping is one **no token can ever satisfy**: the issuer is already registered org-scoped and `orgFieldValue` is empty, or the issuer is single-tenant and `orgFieldValue` is set. The message names the issuer, its org-scoping mode, and which half is missing. Nothing is persisted — the `tenants` row rolls back with the mapping. | Send the `orgFieldValue` the issuer's org claim will actually carry (read the issuer's mode from [`GET /v1/tenant-issuers`](#get-v1tenant-issuers)), or drop `orgFieldValue` for a single-tenant issuer. |
| `500` | Persistence failed — e.g. the tenant `name` already exists (a uniqueness violation), or the request-scoped security context is missing (an internal wiring error). | Use a unique tenant name; if it persists, it is a server bug. |

### `PATCH /v1/tenants/reconcile-bootstrap-name` {#patch-v1tenantsreconcile-bootstrap-name}

The one-way repair for [the bootstrap-name drift above](#tenant-issuers): renames the caller's own `OPERATIONS` tenant to match this instance's declared `ERUN_TENANT`. Deliberately **not** a general tenant-rename API — renaming a tenant that already has environments would break the `<tenant>-<env>` namespace invariant every one of them depends on, so this only ever touches the platform's own tenant, and only while it has none.

Takes **no request body**. The target name is this instance's own server-side config, never a value the caller supplies — that is what keeps the surface narrow instead of a rename primitive a caller could point anywhere:

```jsonc
// 200 response — already the same shape POST /v1/tenants returns
{
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f50",
  "name": "frs",
  "type": "OPERATIONS",
  "createdAt": "2026-08-19T18:12:22Z",
  "updatedAt": "2026-08-30T09:00:00Z"
}
```

If the caller's tenant name already matches `ERUN_TENANT`, this is a no-op success (`200`, tenant unchanged) rather than a refusal — calling it speculatively is always safe.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `403` | The caller's resolved tenant is not `OPERATIONS` (the same explicit gate `POST /v1/tenants` applies). | Call from the platform's own operations-tenant token. |
| `409` | This instance has no `ERUN_TENANT` configured — nothing to reconcile against. | Set `ERUN_TENANT` on the deployment, then retry. |
| `409` | The caller's tenant already has one or more environments — renaming it would break their `<tenant>-<env>` namespace invariant. | Nothing to do: an operations tenant with existing environments cannot be renamed by design. If the name genuinely must change, that is a manual, out-of-band data migration, not this endpoint. |
| `409` | Another tenant already holds the `ERUN_TENANT` name (`tenants.name` is globally unique). | Free or rename the conflicting tenant first, or correct `ERUN_TENANT` to a name that is actually this platform's own. |

### `GET /v1/tenants/reachable` {#get-v1tenantsreachable}

Answers a question no other endpoint does: **which tenants does the calling identity map to**, not which tenant the current token resolved to. Resolution is `(iss, org) → exactly one tenant` per token ([Identity model](#tenant-issuers)), but the same external identity can be enrolled in more than one tenant — `user_external_ids` is keyed `(tenant_id, issuer, external_id)`, so `(tenantA, iss, sub)` and `(tenantB, iss, sub)` are both legal rows for the same human. This endpoint is what lets a caller discover the rest of their own reachable tenants so they can re-authenticate into one of them (see the console's tenant switcher below).

**This is the one endpoint that deliberately crosses the tenant-scoping boundary every other read observes.** It keys the lookup on the caller's own verified `(issuer, subject)` from the token — never a caller-supplied identity — and joins across every `tenant_id` in `user_external_ids`, not just the one the request resolved to. It returns tenant identity only (`tenantId`, `name`, `type`): nothing scoped inside any of those tenants, since the caller is authenticated to the one tenant this request resolved to, not to any of the others being reported. No other route in this API is written this way; do not use it as a precedent for a new cross-tenant read without the same deliberate design pass.

```jsonc
// 200 response — every tenant the caller's own (issuer, subject) maps to,
// including the one this request already resolved to, each annotated with
// whether a sign-in by this same identity can actually produce it
[
  { "tenantId": "019a7fa5-…", "name": "acme",     "type": "COMPANY", "createdAt": "…", "updatedAt": "…", "reachability": "RESOLVABLE" },
  { "tenantId": "019a8b21-…", "name": "beta",     "type": "COMPANY", "createdAt": "…", "updatedAt": "…", "reachability": "ORG_MISMATCH" },
  { "tenantId": "019a8c40-…", "name": "probeco",  "type": "COMPANY", "createdAt": "…", "updatedAt": "…", "reachability": "NO_ORG_MAPPING" }
]
```

**Membership is necessary but not sufficient.** Membership and resolution use **different keys**: a `user_external_ids` row is `(issuer, external_id)`, while sign-in resolves `(iss, org claim)`. Nothing binds the two, so a membership row can name a tenant no token from that identity will ever resolve to. `reachability` is the verdict, computed against the org this caller's own token actually presented:

| `reachability` | Meaning | What resolves it |
|---|---|---|
| `RESOLVABLE` | This identity's `(iss, org)` resolves to this tenant. Signing in again lands here. | — |
| `ORG_MISMATCH` | The tenant resolves for a different org value than this identity presents. The membership row is real and permanently unusable by this account. | An account owned by that tenant's org. Nothing this account can do reaches it. |
| `NO_ORG_MAPPING` | The tenant's mapping for this issuer contradicts the issuer's org-scoping mode, so **no** token can resolve to it — not just this caller's. | An operator repairs the mapping ([`PATCH /v1/tenant-issuers`](#patch-v1tenant-issuers)); the tenant also reports `resolvable: false` on [`GET /v1/tenants`](#get-v1tenants). |
| `ISSUER_NOT_MAPPED` | The tenant has no mapping for the issuer this identity signs in through. | An operator maps the issuer to that tenant. |

**Unresolvable memberships are annotated, never dropped.** Filtering them out of the response would replace one silence (a switch target that always fails) with another — a genuinely misconfigured membership invisible to the operator who has to repair it. A client must offer only `RESOLVABLE` targets and surface the rest with their reason. A membership carrying no `reachability` at all is a platform too old to compute one, which is "cannot say", not "unreachable".

**One account resolves to exactly one tenant.** Under an org-scoped issuer whose org claim is the immutable owner of the account — `urn:zitadel:iam:user:resourceowner:id`, which is what the [bootstrap registers](#tenant-issuers) for an erun-shipped IdP — the claim is not selectable at sign-in. A Zitadel user has exactly one resource owner, so every token that account can ever mint resolves to the same tenant. **Reaching a second tenant therefore needs a second account, owned by that tenant's org** — not a second sign-in with the same account. Enrolling one account into two tenants under such an issuer creates a membership row that can never authenticate; that is the `ORG_MISMATCH` row above, and it is why the switcher does not offer it.

**Switching is a re-authentication, not a re-scope.** Because tenant resolution is a pure function of the token, the console cannot move the active tenant by changing client-side state — it holds a bearer token with no server-side session, and relabeling which tenant it claims to operate on would leave the API still resolving the original tenant from that token. The console's tenant switcher (visible in the app shell whenever this endpoint reports more than one **resolvable** tenant) instead starts a fresh OIDC sign-in with `prompt=select_account`, so the identity provider offers an account/org picker instead of silently reusing the existing browser session, and remembers which tenant it asked for. If the credential that comes back resolves (via `GET /v1/config`) to a different tenant than requested, the console says so and offers to try again — it never claims a switch succeeded that the API disagrees with. That banner is the safety net for a credential the caller picked differently than intended, not the mechanism for reporting an offer that could never have worked.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `500` | The request-scoped security context is missing its verified issuer/subject (an internal wiring error). | Retry with a valid bearer token; if it persists, it is a server bug. |

### `POST /v1/users` and `GET /v1/users` {#post-v1users-and-get-v1users}

Enrolls or lists users. Today the **only** other way a user comes to exist is the per-tenant first-user bootstrap (see [above](#tenant-issuers)) — this endpoint is how an authorized caller enrolls additional users beyond that first one.

Both act on the caller's own resolved tenant by default. An explicit `tenantId` (body field for the `POST`, `?tenantId=` query param for the `GET`) targets a **different** tenant, and is honored only when the caller's resolved tenant is `OPERATIONS` — the same cross-tenant precedent as [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota): a non-operations caller naming another tenant is rejected with `403` before any read or write.

```jsonc
// POST /v1/users body
{
  "username": "alice",              // required
  "issuer": "https://idp.example",  // optional — links the external identity so the enrollee can actually sign in
  "subject": "alice@idp.example",   // optional — required together with issuer
  "tenantId": "019a…",              // optional — operations-only cross-tenant target
  "roleIds": ["019a7fa5-…-f70"]     // optional — grant these roles instead of the zero-role default
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

Omitting `issuer`/`subject` enrolls a username with **no external identity yet** — the row exists, but no token can resolve to it until one is linked, and there is no separate endpoint to link one after the fact in this build.

**Re-enrolling an already-linked identity is a no-op, not a conflict.** When `issuer`/`subject` are given and that exact pair is already enrolled in the target tenant, the response is `200` (not `201`) with `alreadyEnrolled: true` and the user that already holds the mapping — its real `username`, which may differ from the one this request asked for. The operator's intent ("this identity usable in this tenant") was already satisfied, so nothing is created and existing roles are left untouched:

```jsonc
// 200 response — issuer/subject already enrolled as a different username
{
  "userId": "019a7fa5-c2c0-7c55-bc70-714873a71f60",
  "tenantId": "019a7fa5-c2c0-7c55-bc70-714873a71f10",
  "username": "alice@idp.example",  // the username already on file, not the one requested
  "issuer": "https://idp.example",
  "subject": "alice@idp.example",
  "alreadyEnrolled": true,
  "createdAt": "2026-06-24T10:00:00Z",
  "updatedAt": "2026-06-24T10:00:00Z"
}
```

**Role assignment default.** An enrolled user gets exactly the roles named in `roleIds`. Omitting `roleIds` grants `TenantUser` — enough to read the tenant, drive reviews/comments/builds/the merge queue, and operate environments that already exist — rather than the zero-role default this endpoint shipped briefly, which left an invited colleague unable even to read [`GET /v1/whoami`](#get-v1whoami) until someone remembered to grant a role by hand. The one exception is the target tenant's **first** user: that enrollment gets `TenantAdmin` regardless of `roleIds`, because granting a role is itself a permission-gated call, so a first (and only) user holding nothing could never be granted anything. [`GET`/`POST /v1/roles`](#roles-endpoints) and [`/v1/users/{user_id}/roles`](#roles-endpoints) below are how an operator lists existing roles and grants/revokes them after enrollment.

This endpoint requires the caller to already know the enrollee's `issuer`/`subject` from the identity provider. [`POST /v1/identity/users`](/agent-reference/identity-administration) is the higher-level alternative for a platform running its own IdP (Zitadel): it creates the IdP identity itself and calls this same mapping with the subject the IdP returns, in one action, restricted to an `OPERATIONS` tenant.

**What the enrollment refusal does and does not check.** The endpoint refuses an enrollment whose *target tenant's mapping* no token can resolve through (`UNRESOLVABLE_ISSUER_MAPPING`, below) — a fact the platform owns entirely, since it holds both the issuer's org-scoping mode and the mapping's org value. It does **not** check whether the org that owns `subject` matches the target tenant's org, because `(issuer, subject)` alone does not tell the platform which org owns that account, and refusing on a guess would reject legitimate enrollments. That mismatch is therefore reported rather than refused: it surfaces as `ORG_MISMATCH` on [`GET /v1/tenants/reachable`](#get-v1tenantsreachable) for the enrolled identity, so a client never offers it as a switch target. Under an org-scoped issuer whose claim is the account's immutable owner, enrolling one account into a second tenant is exactly this case — see that endpoint's "One account resolves to exactly one tenant".

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

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `username` is empty, or the body is not valid JSON. | Send a non-empty `username`. |
| `403` | `tenantId` (or `?tenantId=`) names a different tenant than the caller's own, and the caller's resolved tenant is not `OPERATIONS`. | Omit `tenantId` to act on your own tenant, or call from an operations-tenant token. |
| `404` | `POST /v1/users`: a `roleIds` entry does not name a role in the target tenant. | Fix the role id, or create the role first via [`POST /v1/roles`](#roles-endpoints). |
| `409` `USERNAME_TAKEN` | `POST /v1/users`: a *different* identity already holds that `username` in the target tenant (`users_tenant_username_key`). Re-enrolling the *same* `issuer`/`subject` that already holds a username is never this — see the `200`/`alreadyEnrolled` response above. | Use a different username, or omit `tenantId` if you meant your own tenant. |
| `409` `UNRESOLVABLE_ISSUER_MAPPING` | `POST /v1/users` with `issuer`/`subject`: the target tenant's mapping for that issuer is one **no token can resolve through** — its org value contradicts the issuer's org-scoping mode, or the tenant has no mapping for that issuer at all. The enrollment would produce a user who can never sign in. The whole transaction rolls back, so no `users` row is left behind. | Repair the tenant's mapping first ([`PATCH /v1/tenant-issuers`](#patch-v1tenant-issuers), or check it with [`GET /v1/tenant-issuers`](#get-v1tenant-issuers)); a tenant in this state also reports `resolvable: false` on [`GET /v1/tenants`](#get-v1tenants). |
| `409` `CONFLICT` | `POST /v1/users`: a uniqueness violation this endpoint does not recognize as either of the above. | Retry is unlikely to help without changing the request; treat as a server-side gap and report it. |

### Roles and role assignment {#roles-endpoints}

A **role** is a named, tenant-owned bundle of permissions; a **permission** is one API method + path grant, either an exact pair (`apiMethod`/`apiPath`) or a regex pattern pair (`apiMethodPattern`/`apiPathPattern`) — the same shape `role_permissions` stores and [the capability set](#capability-set) resolves against. `ReadAll` and `WriteAll` are two such predefined roles, created per tenant on first use and granted automatically only to a tenant's bootstrap first user (see [`POST /v1/users`](#post-v1users-and-get-v1users) above) — every later enrollment holds nothing until someone grants it a role, predefined or custom. All five endpoints below act on the caller's own resolved tenant — RLS scopes every read and write, and there is no operations-tenant cross-tenant override (unlike `/v1/users`).

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

**Error behaviour.** Same generic JSON `{code, message}` envelope as `/v1/users`:

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

**Cross-tenant administration (#1816).** For the Operator view, see [Administering another tenant](/collaboration/cross-tenant-administration). This write is recorded in a second, explicit audit event (beyond the ordinary per-request one every API call gets) naming the target tenant, the operator, and the operator's own home tenant, so the write is attributable even though the row itself, once persisted, only ever names the target — the same shape [`POST /v1/environments`](#post-v1environments) uses for its own cross-tenant create. The set is refused before it runs if audit logging cannot be recorded, rather than silently changing another tenant's caps with no attributable record of who did it. [`GET /v1/quota`](#get-v1quota) below takes the same target tenant, as a `?tenantId=` query param, so a quota can be seen before it is set.

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

**What the resource caps mean.** `maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb` are a **per-environment namespace ceiling**, not an aggregate tenant budget: every `runtime` environment this tenant provisions gets its own Kubernetes `ResourceQuota` + `LimitRange` capped at these same values (see [Quotas](/concepts/hosted-platform#quotas)), so a tenant with ten environments can use up to this cap in *each* of the ten namespaces, not this cap split across all ten. `maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb` are the separate **aggregate tenant-wide budget**: since every environment gets the identical per-environment cap, admission projects `(existing runtime environment count + 1) × the per-environment cap` against this budget and refuses a create that would exceed it (a redeploy uses the count as-is, since it does not add one). Absent a `tenant_quotas` row, a tenant gets the default cap: `maxEnvironments: 10`, `maxCpuMillicores: 8000`, `maxMemoryMb: 29396`, `maxStorageGb: 72` — sized to fit the `erun-devops` chart's own default runtime pod summed across **both** its containers (`erun-devops` cpu limit `4` + memory limit `8916Mi`, plus the `erun-dind` sidecar at cpu limit `4` + memory limit `20Gi` — the sidecar's own default is larger, since every image build's `make check` gate runs there) plus its three default PVCs (`2Gi + 50Gi + 20Gi = 72Gi`) — and `maxTotalCpuMillicores: 80000`, `maxTotalMemoryMb: 293960`, `maxTotalStorageGb: 720` (`maxEnvironments` × the per-environment defaults, so the default budget accommodates the default environment-count cap at the default per-environment size). Setting either resource cap below this floor is accepted here (an operator may deliberately want a tenant that cannot provision runtime environments yet), but the next [`POST /v1/environments`](#post-v1environments) or [`POST .../deploy`](#deploy-endpoint) for that tenant then refuses with `409` rather than letting the create/deploy proceed toward a pod Kubernetes will never admit.

**Error behaviour.** Bare HTTP status with the generic JSON `{code, message}` envelope (see [Errors](#errors)) — `code` is the status-derived default (e.g. `NOT_FOUND`, `CONFLICT`); none of the [Reviews-specific machine codes](/collaboration/reviews#machine-error-codes) apply here:

| Status | Condition | Recovery |
|---|---|---|
| `400` | `tenant_id` is empty, `maxEnvironments` is negative, any of `maxCpuMillicores`/`maxMemoryMb`/`maxStorageGb`/`maxTotalCpuMillicores`/`maxTotalMemoryMb`/`maxTotalStorageGb` is `<= 0` (a PUT replaces the whole row, so these must be sent explicitly on every call), or the body is not valid JSON. | Send a non-negative `maxEnvironments` and positive resource caps. |
| `403` | The caller's resolved tenant is not an `OPERATIONS` tenant (the explicit operations gate). | Call from an operations-tenant token. |

### `GET /v1/quota` {#get-v1quota}

Returns the caller's own tenant's full quota row by default — the identical shape and defaulting [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) writes and [`POST /v1/environments`](#post-v1environments) admission itself reads. **Tenant-self-service**: no operations role required, and the read is scoped explicitly to the caller's own tenant rather than left to row-level security alone. This is how an Operator inspects their own limits without an operations-scoped token (#605, #1113).

**Cross-tenant read (#1816).** An operations-scoped caller may instead read another tenant's quota via `?tenantId=<tenant_id>`, the same `resolveTargetTenant` convention [`GET /v1/environments`](#post-v1environments), [`GET /v1/users`](#post-v1users-and-get-v1users), and [`GET /v1/invites`](#invites) use: omitted or equal to the caller's own tenant reads the caller's own row; a different value is refused with `403` for any caller whose tenant is not `OPERATIONS`. This is the read half of [`PUT /v1/tenants/{tenant_id}/quota`](#put-v1tenantstenant_idquota) above — until this existed, an operator could set another tenant's quota without ever being able to see its current value first.

```jsonc
// GET /v1/quota?tenantId=019a… (operations-only cross-tenant; omit tenantId for the caller's own tenant)

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

| Status | Condition | Recovery |
|---|---|---|
| `403` | `?tenantId=` names a different tenant than the caller's own, and the caller's resolved tenant is not `OPERATIONS`. | Omit `tenantId` to read your own tenant's quota, or call from an operations-tenant token. |

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

Every `401` the auth layer produces carries a JSON `{code, message}` envelope (the same shape `writeErrorCode` uses elsewhere in the API — see `internal/routes/errors.go`); `403` (a permission denial, not an auth-resolution failure) is still a bare status with a plain-text body. `message` is the underlying reason (which issuer, which claim, or its absence) in prose; server logs also carry it. The shipped contract:

| Status | `code` | Example `message` | Condition | Recovery |
|---|---|---|---|---|
| `401` | `UNAUTHENTICATED` | `missing bearer token` | No `Authorization` header, or it is not a single `Bearer <jwt>` pair. | Send `Authorization: Bearer <jwt>`. |
| `401` | `UNAUTHENTICATED` | `invalid bearer token` | Signature/claims (`exp`/`nbf`/`iat`) failed, token expired, the issuer is not on the allow-list, or `iss`/`sub` is empty. | Re-mint a valid token from a registered issuer. |
| `401` | `TENANT_UNRESOLVED` | `tenant could not be resolved from token: issuer "…" is org-scoped (claim "…") but the token carries no matching claim` | Token verified and the issuer is known, but `(iss, org)` could not be resolved to a tenant — most commonly an org-scoped issuer whose token carries no matching org claim, or one whose claim value matches no registered tenant. **Distinct from `NOT_ENROLLED`**: the caller may already be a member of a tenant this exact token simply cannot resolve to, so "ask an operator to enrol you" would be the wrong advice (erun#1721). | Request the scope that puts the claim on the token (see [the org-scoped issuer note](#tenant-issuers) above), or check the org value against [`GET /v1/tenant-issuers`](#endpoints). |
| `401` | `NOT_ENROLLED` | `record not found` | The tenant resolved fine, but no `user_external_ids` row matches `(tenant, iss, sub)`, and the tenant already has users (so per-tenant first-user bootstrap does not apply). | Enrol the subject for the tenant (`POST /v1/users`). |
| `401` | `RESOLUTION_FAILED` | `identity could not be resolved because of an internal error` | Resolution hit an unexpected database error (e.g. two callers racing to become a zero-user tenant's first user). **Distinct from both `TENANT_UNRESOLVED` and `NOT_ENROLLED`**: this is not a real answer about enrolment or tenant resolution, so neither "ask an operator to enrol you" nor "you may already be enrolled elsewhere" applies — the underlying database detail (constraint name, SQLSTATE) is logged server-side only and never reaches this message (erun#1752). | Retry; if it persists, check the server log for the sanitized reason. |
| `401` | `UNAUTHORIZED` | (varies) | A resolution failure not covered by any case above — e.g. an entirely unregistered issuer with no empty `tenants` table left to bootstrap into. | Register the issuer, or check the server log for the underlying reason. |
| `403` | *(none — plain text)* | `Forbidden` | Authenticated, but the user's roles/permissions do not allow the request's method + path. | Grant the needed role/permission (admin action). |

The audit trail records every authorized request with `issuer`, `sub`, org, and timestamp. Rejected requests (missing/invalid token, unknown issuer, unresolved tenant, unenrolled subject, denied permission) are **not** audited — see [the audit log spec](/agent-reference/audit-log).

#### Structured error codes `(Planned.)`

The resolution-level codes above (`UNAUTHENTICATED`/`TENANT_UNRESOLVED`/`NOT_ENROLLED`/`RESOLUTION_FAILED`/`UNAUTHORIZED`) are shipped. This deeper, JWT-verification-specific catalogue — plus the codes the still-unimplemented self-service trust-management API would return — is **not implemented yet**; a client must not branch on these:

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

## Rate limits `(Planned.)` {#rate-limits}

**Not implemented yet.** There is no request-rate limiter anywhere in `erun-backend-api` today — no per-token bucket, no per-tenant aggregate, and no `429` response on any path. A caller can call as fast as it wants; the only ceiling is ordinary HTTP concurrency and the database beneath it. The design below is the target shape, kept here so a client that wants to be a good citizen ahead of time can already structure its retry logic around `Retry-After`/`RateLimit-*` — but nothing enforces it, and no client should treat a `429` as a documented possibility today.

| Bucket | Limit | Notes |
|---|---|---|
| Per-token, read endpoints | 600 req/min | `GET` on reviews, comments, builds, whoami, tenant-issuers. |
| Per-token, write endpoints | 60 req/min | `POST` / `PATCH` on reviews, comments, builds. |
| Per-token, merge-queue advance | 10 req/min | `POST /v1/reviews/merge-queue/advance`. Tightened because each call mutates shared state. |
| Per-tenant aggregate | 1500 req/min | Sum of all tokens belonging to the tenant. |

Once implemented, hitting a limit would return `429 Too Many Requests` with:

```
Retry-After: <seconds>
RateLimit-Limit: <bucket limit>
RateLimit-Remaining: 0
RateLimit-Reset: <unix epoch>
```

## Pagination `(Planned.)` {#pagination}

**Not implemented yet.** List endpoints (`GET /v1/reviews`, `GET /v1/reviews/{id}/comments`, `GET /v1/reviews/{id}/builds`, and every other `GET` list route in [Endpoints](#endpoints)) return the **entire result set in one response** today — no page size cap, no `items`/`nextPageToken` envelope, and no `pageToken` query parameter accepted anywhere. A caller that sends `?pageToken=...` has it silently ignored (list routes read only the filter query params each one documents). The shape below is the target design:

```jsonc
{
  "items": [ /* … */ ],
  "nextPageToken": "eyJvZmZzZXQiOjEwMH0="
}
```

Once implemented, the token would be passed back as `?pageToken=<token>` on the next call, with a stale token refused as `400 Bad Request` (code `EXPIRED_PAGE_TOKEN`). Until then, a tenant with a very large number of reviews/comments/builds gets them all back in one call — there is no signal that more exist because there never is more than what came back.

## See also

- [Reviews](/collaboration/reviews) — resource schema + lifecycle.
- [Comments](/collaboration/comments) — resource schema + threading.
- [Builds](/collaboration/builds) — resource schema + append-only semantics.
- [Audit log event format](/agent-reference/audit-log#event-shape).
- [Security events](/agent-reference/audit-log#security-events).
- [Container registries · Hosted registry](/deployment/registries#hosted-registry) — the Operator-facing summary of the hosted registry this endpoint authenticates.
