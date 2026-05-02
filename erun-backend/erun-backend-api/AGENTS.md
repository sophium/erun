# AGENTS.md

Module-specific guidance for `erun-backend-api`. Follow the repository root and `erun-backend/AGENTS.md` first.

## Module Role

- `erun-backend-api` is the Go HTTP API module for hosted ERun backend functionality.
- API endpoints must require an OIDC bearer token unless the endpoint is explicitly infrastructure-only, such as a future health check.
- The token `iss` claim determines the tenant. Resolve the tenant before invoking endpoint behavior, then pass tenant identity explicitly through request-scoped context.
- Keep endpoint handlers thin: authenticate, adapt HTTP inputs, call focused workflow or persistence code, and return JSON-safe responses.
- Do not let CLI or MCP import this module directly. Shared clients, request contracts, and result contracts used by CLI and MCP belong in `erun-common`.

## Layer Layout

- Keep each API layer in its own directory under `internal/`.
- Put DB-mapped entities in `internal/model/`.
- Put SQL persistence code in `internal/repository/`.
- Put workflow orchestration in `internal/service/` only when a workflow has real logic beyond calling one repository method.
- Put HTTP route registration, request parsing, and response writing in `internal/routes/`.
- Keep `server.go` as the composition boundary. It should construct repositories, optional services, routes, and middleware, then wire them together.
- Do not create layer directories as empty abstractions. Add a service file only when a service owns behavior.
- Keep imports directional: routes may import repositories or services and model; services may import repositories and model; repositories may import model; model must not import API layers.

## Model Entities

- `internal/model` is the shared entity language for all API layers.
- Model structs should map to database tables, for example `model.Review` for `reviews` and `model.Comment` for `comments`.
- Do not create separate repository entities, service entities, domain entities, DTOs, or response entities that mirror the same table fields.
- Repositories should scan SQL rows directly into `model` structs.
- Services should accept and return `model` structs when they are working with table-backed entities.
- Routes may return `model` structs directly when the API shape matches the entity.
- Route-local request structs are allowed for partial inputs such as create requests, status updates, filters, or path/body combinations that are not full table entities.
- Do not add entity-to-entity mapping just to cross a layer boundary.

## Repository Layer

- Repositories own SQL and persistence behavior.
- Repository methods should use `context.Context` and return `model` values directly.
- Repository methods should not parse HTTP, write JSON, inspect headers, or know route names.
- Repository methods should not receive security filter arguments such as tenant ID or user ID just to satisfy RLS. Security context setup belongs to shared transaction wiring.
- Tenant-owned SQL should run inside repository transaction wiring that reads the authenticated security context from `context.Context`.
- For PostgreSQL, transaction wiring must set `erun.tenant_id` with transaction-local scope before tenant-owned SQL runs.
- For PostgreSQL operations that depend on user ownership, such as closing comments, transaction wiring must also set `erun.user_id`.
- Use transaction-local settings such as `set_config(..., true)` so values do not leak through pooled connections.
- Missing security context inside repository transaction setup is an internal wiring error for protected routes, not a user authentication failure.
- SQLite repositories should still write table-required tenant/user fields through the `model` values and use normal predicates where needed, but SQLite is not the PostgreSQL RLS authority.

## Service Layer

- Do not add a service method that simply calls one repository method with the same inputs and outputs.
- Routes may call repositories directly for simple CRUD or simple lookups.
- Add a service only when it owns real workflow behavior, such as coordinating multiple repositories, applying state-transition rules, deriving child comment fields from a root comment, writing audit events with another action, or running multiple writes in one transaction.
- Services should remain transport-neutral. They should not read HTTP headers, path values, or request bodies.
- Services should use `model` structs as their entity language and avoid layer-specific entity copies.

## Routes Layer

- Routes own HTTP adaptation: path values, query values, request body decoding, status codes, and JSON responses.
- Protected routes should be registered behind authentication middleware and may assume authenticated security context exists.
- Do not repeat `SecurityFromContext` checks in every protected route just to reject invalid users. Authentication middleware must reject invalid, missing, or unknown users before route code runs.
- If a route needs authenticated identity as user-visible input, prefer a small helper that treats missing context as an internal wiring error.
- Keep route request structs local to `internal/routes` unless the request contract is intentionally shared with CLI or MCP through `erun-common`.
- Avoid route-to-model response mapping when the model already has the correct JSON shape.

## Reviews And Builds

- Review `name` is the squash merge message.
- Reviews must have both `targetBranch` and `sourceBranch`.
- Review status values include `OPEN`, `CLOSED`, `FAILED`, `READY`, `MERGE`, and `MERGED`; do not remove existing statuses when adding workflow states.
- Successful builds should move `OPEN` or `FAILED` reviews into the per-target-branch merge queue as `READY`.
- A `READY` review becomes `MERGE` only when it is advanced as the next item for that target branch.
- If a `MERGE` review misses its merge window without failing, move it back to `READY` at the end of the same target branch queue.
- Failed builds for queued or merging reviews should move the review to `FAILED` and remove it from the queue.
- `CLOSED` reviews must not appear in the merge queue.
- Review list endpoints should support filtering by target branch.

## Authentication

- Validate bearer tokens before resolving tenant state.
- Treat missing, malformed, or unverifiable bearer tokens as unauthorized.
- Treat an unknown issuer as unauthorized because it cannot be mapped to an ERun tenant.
- Resolve the ERun user from the token issuer and subject before protected route code runs.
- Store tenant ID, ERun user ID, external issuer, and external user ID in request-scoped security context.
- Avoid package-level mutable auth configuration. Pass verifiers and tenant resolvers through explicit API construction.
- Prefer a single identity resolver when database-backed auth is used, because empty-database bootstrap must resolve or create tenant, issuer, user, roles, and permissions atomically.
- If there are no tenants, the first valid authenticated identity may create the initial `OPERATIONS` tenant and first ERun user. That user must receive both predefined roles: `ReadAll` and `WriteAll`.
- Once any tenant exists, unknown issuers or unknown external subjects are unauthorized and must not create users implicitly.
- Operations tenants are system tenants. PostgreSQL RLS allows them to access tenant-owned rows across tenants by setting the transaction role to `erun_operations`, but API authorization must still require assigned roles and permissions.
- Normal tenant transactions must use PostgreSQL role `erun_tenant`; operations tenant transactions must use PostgreSQL role `erun_operations`.

## Authorization

- Permissions are stored in `role_permissions` as either exact API method/path pairs or regex method/path patterns.
- Permission matching must use the canonical API path template set by route registration, such as `/v1/reviews/{review_id}`, not the concrete request URL.
- Keep broad predefined roles pattern-based: `ReadAll` covers all read-style methods across all API paths, and `WriteAll` covers all write-style methods across all API paths.
- Route handlers should not check role names directly. Authorization middleware should compute access from the authenticated user's assigned roles and permissions before the route handler runs.

## Identity Resolution Cache

- Authentication middleware may cache issuer, external subject, tenant, and ERun user resolution results with a bounded TTL.
- Cache both successful and failed external identity lookups.
- Failed external identity lookup caching is required so repeated requests with invalid external IDs do not repeatedly hit the database.
- Keep negative-cache TTL short enough that newly created users become usable without a long delay.
- Keep positive-cache TTL bounded so tenant issuer and user mapping changes converge without restarting the API.
- Key identity caches by issuer and external subject. Do not cache only by subject because subjects are issuer-scoped.
- Do not cache raw bearer tokens as identity keys.
- Do not let identity cache decisions bypass token verification. Verify the bearer token first, then use claims to look up cached tenant/user resolution.
- Cache entries must be safe for concurrent requests and must not use package-level mutable globals. Pass cache instances through API construction.
- Expose cache TTLs as explicit configuration when they become runtime-tunable.

## Audit Logging

- Authentication or request middleware should write audit events for successfully authorized API requests.
- Do not make individual route handlers responsible for routine audit logging.
- Write audit events only after token verification, tenant resolution, user resolution, and endpoint authorization have succeeded.
- API audit events must include tenant ID, ERun user ID, external issuer, external user ID, type `API`, API method, canonical API path, and event time.
- The audit API path must be the same canonical route template used by `role_permissions.api_path`, such as `/v1/reviews/{review_id}`, not a concrete URL containing IDs or query strings.
- Use the canonical route path and method observed by middleware as the audit source. Do not let routes hand-write those values for normal request auditing.
- Register protected routes through the shared route registration helper so the canonical API path is available to authentication, authorization, and audit middleware.
- Audit logging should use the same request-scoped security context that repository transaction wiring uses.
- Audit writes should target SQLite for simple deployments and ClickHouse for normal deployments, matching `erun-backend-db` schema guidance.
- Future CLI and MCP audit callers must set type `CLI` or `MCP` and populate `cli_command` or `mcp_tool` respectively. Store parameter payloads as serialized text, preferably compact JSON for structured input.
- Treat audit logging failure policy as an explicit API configuration decision. Default request auditing should prefer failing closed only when the endpoint requires audit durability; otherwise log/report the audit failure without hiding authorization failures as unrelated route errors.
- Do not log audit events for requests rejected before authorization, such as missing tokens, invalid tokens, unknown issuers, unknown external users, or denied permissions.

## IDs

- Use UUIDv7 for all externally visible API IDs.
- Keep ID generation in one owned helper rather than scattering UUID creation across handlers.

## Validation

- Run `go test ./...` from this module after Go changes.
