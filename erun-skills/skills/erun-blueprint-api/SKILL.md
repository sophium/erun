---
name: erun-blueprint-api
description: Build or maintain a multi-tenant Go HTTP API service following ERun's blueprint — OIDC bearer authentication, tenant resolution from the token issuer, layered model / repository / service / routes structure, transaction-scoped PostgreSQL security context, identity resolution cache, and audit logging — and reconcile, repair, and upgrade a previously scaffolded service in place by realigning it to the current blueprint and refreshing the service's own dependency pins, without clobbering the project's own business logic (it is a standalone Go module with no erun-version coupling). Captures the patterns that erun-backend-api packages. Use when the user says "build a multi-tenant http api", "create an erun-backend-api-shaped service", "I need a multi-tenant Go api with oidc auth and tenant rls", "build a multi-tenant backend api", "upgrade the multi-tenant api", "repair the erun-backend-api-shaped service", "reconcile the api to the blueprint", "bump the api's dependencies", "maintain the multi-tenant api", or any similar request for a new or existing multi-tenant Go API.
---

# Build a multi-tenant API service

Produce a Go HTTP API service following ERun's blueprint — the same
shape `erun-backend-api` captures: OIDC bearer authentication, tenant
resolution from the token `iss` claim, layered `internal/model` →
`internal/repository` → `internal/service` → `internal/routes`
structure, transaction-scoped PostgreSQL security context
(`SET LOCAL ROLE erun_tenant` / `SET LOCAL erun.tenant_id`), bounded
identity-resolution cache, and audit logging on successfully authorized
requests.

This skill packages ERun's accumulated best practices for multi-tenant
HTTP APIs. Do not freelance the patterns; the conventions encoded here
are the contract.

## When to use

Trigger on user phrasings such as:

- "build a multi-tenant http api" / "build a multi-tenant backend api"
- "create an erun-backend-api-shaped service"
- "I need a multi-tenant Go api with oidc auth and tenant rls"
- "I need an HTTP API that talks to my erun-backend-db-shaped database"

## Prerequisite

This skill assumes the database side already exists or is being built
in parallel via the `erun-blueprint-rls-db` skill. The API expects:

- A PostgreSQL database with `erun_tenant` and `erun_operations` roles.
- An `erun_current_tenant_id()` function backed by session setting
  `erun.tenant_id`.
- The bootstrap tables: `tenants`, `tenant_issuers`, `users`,
  `user_external_ids`.
- An `audit_events` table for API/MCP/CLI audit (add via the db skill
  if missing).

If the database side is not present, run `erun-blueprint-rls-db` first
or confirm with the user that they have an equivalent.

## Inputs to collect

1. **Module name** (e.g. `acme-api`). Used as the Go module name and
   directory name.
2. **Go module path** (e.g. `github.com/acme/acme-api`). Used in
   `go.mod` and import paths.
3. **Target directory** (default: current working directory).
4. **OIDC issuers to accept initially** — list of issuer URLs. The
   first authenticated identity from any of them bootstraps the
   OPERATIONS tenant on an empty database; subsequent unknown issuers
   are rejected.
5. **Initial domain entities** — list of tenant-owned entities (e.g.
   `invoices`, `customers`). For each: route prefix (e.g.
   `/v1/invoices`), minimal columns. Empty list is fine — the produced
   module still has a working whoami endpoint.

Ask once, then proceed.

## What gets produced

```
<module-name>/
├── AGENTS.md
├── go.mod
├── cmd/
│   └── <module-name>/
│       └── main.go                          # process entrypoint
├── server.go                                # HandlerOptions + NewHandler composition
├── auth.go                                  # AuthMiddleware
├── oidc.go                                  # OIDC TokenVerifier (issuer + JWKS)
├── identity_cache.go                        # Identity resolution cache
├── audit.go                                 # AuditLogger interface
├── api_path.go                              # canonical API path helpers
└── internal/
    ├── model/
    │   ├── user.go
    │   └── <user-supplied entities>.go
    ├── repository/
    │   ├── tx.go                            # TxManager — security context wiring
    │   ├── identity.go                      # IdentityRepository — issuer/subject resolution + bootstrap
    │   ├── user.go                          # UserRepository — non-bootstrap user CRUD
    │   ├── audit_event.go                   # AuditEventRepository
    │   ├── permission_authorizer.go         # role_permissions matcher
    │   └── <user-supplied entities>.go
    ├── service/
    │   └── (add a file only when a workflow has real logic)
    └── routes/
        ├── protected.go                     # ProtectedRouteRegistrar type
        ├── whoami.go                        # /v1/whoami
        └── <user-supplied entities>.go
```

Reference files for the canonical blueprint ship alongside this
`SKILL.md` under `templates/`. Substitute placeholders, then expand for
the user's domain entities.

This tree is the service's source only. Step 6 below composes
`erun-blueprint-service` to add the missing
`<tenant>-devops/docker/<module>/Dockerfile` and
`<tenant>-devops/k8s/<module>/` chart — without them `erun build`/
`erun deploy` have nothing to find.

## Conventions (binding)

These come from `erun-backend/erun-backend-api/AGENTS.md`. Apply every
one.

### Layer layout

- `internal/model/` — DB-mapped entity structs. Used as the shared
  entity language across all layers. No DTOs, no service entities, no
  route response types that mirror the same fields.
- `internal/repository/` — SQL persistence via Bun. CRUD only. No
  workflows. `Create`/`Get`/`List`/`Update`; add `Delete` only when the
  product hard-deletes.
- `internal/service/` — workflow orchestration **only when a workflow
  has real logic beyond calling one repository method**. Do not create
  the directory until a real service exists.
- `internal/routes/` — HTTP adaptation: path values, query parsing,
  body decoding, status codes, JSON responses.
- `server.go` is the composition boundary — constructs repos, optional
  services, routes, middleware, wires them.
- Import direction: routes → (services or repositories) → model.
  `model` must not import anything from the API layers.

### Authentication

- OIDC bearer token required on every protected route.
  `cmd/<name>/main.go` wires the `TokenVerifier` (JWKS-backed,
  multi-issuer).
- Authentication middleware: verify token → look up tenant from
  `iss` → look up user from `(iss, sub)` → populate request-scoped
  security context → call next.
- An unknown issuer is unauthorized — it cannot be mapped to a tenant.
- An unknown `(iss, sub)` on an empty database may bootstrap the
  OPERATIONS tenant and first user with `ReadAll` + `WriteAll`. Once
  any tenant exists, unknown identities are rejected, no implicit user
  creation.
- Treat missing security context inside repository transaction setup
  as an **internal wiring error**, not a user-facing 401.

### Tenant transaction wiring

Repositories never accept `tenant_id` as an argument. Instead,
`TxManager` opens a transaction and sets the PostgreSQL security
context from the authenticated context:

```go
// Sketch — see templates/repository_tx.go.tmpl for the full version.
func (tm *TxManager) WithTx(ctx context.Context, fn func(*bun.Tx) error) error {
    sec, err := SecurityFromContext(ctx)
    if err != nil {
        return fmt.Errorf("backend: missing security context: %w", err)
    }
    return tm.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
        role := "erun_tenant"
        if sec.IsOperations {
            role = "erun_operations"
        }
        if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+role); err != nil {
            return err
        }
        if _, err := tx.ExecContext(ctx, "SELECT set_config('erun.tenant_id', ?, true)", sec.TenantID.String()); err != nil {
            return err
        }
        if sec.UserID != uuid.Nil {
            if _, err := tx.ExecContext(ctx, "SELECT set_config('erun.user_id', ?, true)", sec.UserID.String()); err != nil {
                return err
            }
        }
        return fn(&tx)
    })
}
```

Use Bun `?` placeholders inside `set_config(...)`. Do not use `$1`
PostgreSQL placeholders in Bun-managed queries.

### Authorization

- Permissions live in `role_permissions` as exact `(method, path)`
  pairs or regex `(method_pattern, path_pattern)` patterns.
- Authorization middleware computes the user's effective permissions
  as the distinct union across all assigned roles, then matches the
  canonical route template (e.g. `/v1/invoices/{invoice_id}`) — not
  the concrete request URL.
- The `ReadAll` predefined role grants all read-style methods across
  all paths; `WriteAll` grants all write-style methods. Route handlers
  never check role names directly.

### Identity resolution cache

- Bounded TTL, positive and negative entries.
- Key by `(issuer, external_subject)`. Never by subject alone.
- Never key by raw bearer token.
- Identity cache decisions never bypass token verification — verify
  first, then look up cached resolution.
- Cache instance passed through API construction; no package-level
  mutable globals.

### Audit logging

- Audit middleware writes events **after** token verification, tenant
  resolution, user resolution, and authorization all succeed.
- Routes never hand-write routine audit events. They are
  middleware-owned.
- Required fields: `tenant_id`, `erun_user_id`, `external_user_id`,
  `external_issuer_id`, `type = 'API'`, `api_method`, `api_path`
  (canonical template, not concrete URL), `created_at`.
- Audit failures on a non-audit-durability endpoint should log and
  continue; the audit failure should not surface as a route 500.

### Canonical API paths

- Use the route template (`/v1/invoices/{invoice_id}`) for permission
  matching and audit logging — never the concrete request URL.
- `api_path.go` wires the canonical path into request context at
  registration time, so authentication/authorization/audit middleware
  can read it.

### IDs

- All externally visible IDs are UUIDv7. Generation lives in database
  defaults (`uuidv7()`), not application code. Create routes and
  create repository methods must not accept or generate IDs.

## Step-by-step

### Step 1 — confirm inputs

Read back: module name, Go module path, target dir, OIDC issuers,
initial entities. Ask before producing if anything is unclear.

### Step 2 — create directory tree

```sh
mkdir -p "${target_dir}/${module}"/{cmd/${module},internal/model,internal/repository,internal/routes}
```

Skip `internal/service/` until a real service exists.

### Step 3 — write boilerplate

Copy the reference files verbatim, substituting `__MODULE__` (e.g.
`acme-api`) and `__MODULE_PATH__` (e.g. `github.com/acme/acme-api`):

- `templates/go.mod.tmpl` → `${module}/go.mod`
- `templates/cmd_main.go.tmpl` → `${module}/cmd/${module}/main.go`
- `templates/server.go.tmpl` → `${module}/server.go`
- `templates/auth.go.tmpl` → `${module}/auth.go`
- `templates/oidc.go.tmpl` → `${module}/oidc.go`
- `templates/identity_cache.go.tmpl` → `${module}/identity_cache.go`
- `templates/audit.go.tmpl` → `${module}/audit.go`
- `templates/api_path.go.tmpl` → `${module}/api_path.go`
- `templates/repository_tx.go.tmpl` → `${module}/internal/repository/tx.go`
- `templates/repository_identity.go.tmpl` → `${module}/internal/repository/identity.go`
- `templates/repository_user.go.tmpl` → `${module}/internal/repository/user.go`
- `templates/repository_audit_event.go.tmpl` → `${module}/internal/repository/audit_event.go`
- `templates/repository_permission_authorizer.go.tmpl` → `${module}/internal/repository/permission_authorizer.go`
- `templates/routes_protected.go.tmpl` → `${module}/internal/routes/protected.go`
- `templates/routes_whoami.go.tmpl` → `${module}/internal/routes/whoami.go`
- `templates/model_user.go.tmpl` → `${module}/internal/model/user.go`
- `templates/AGENTS.md.tmpl` → `${module}/AGENTS.md`

### Step 4 — produce user-supplied entities

For each entity `E` the user supplied (e.g. `invoice`, plural
`invoices`, route prefix `/v1/invoices`):

- `internal/model/<entity>.go` — Bun-tagged struct with read-only
  `EntityID`, `TenantID`, `CreatedAt`, `UpdatedAt`, plus the user's
  domain columns. Use `templates/model_entity.go.tmpl` as the shape.
- `internal/repository/<entity>.go` — `Create`/`Get`/`List`/`Update`
  methods using Bun via `TxManager.WithTx`. Use
  `templates/repository_entity.go.tmpl`.
- `internal/routes/<entity>.go` — `RegisterEntityRoutes` that wires
  `GET /v1/<entity-plural>`, `POST /v1/<entity-plural>`,
  `GET /v1/<entity-plural>/{<entity>_id}`,
  `PATCH /v1/<entity-plural>/{<entity>_id}` through the
  `ProtectedRouteRegistrar`. Use `templates/routes_entity.go.tmpl`.

Only add `internal/service/<entity>.go` when a workflow exists beyond
single-repository CRUD.

### Step 5 — wire entity routes in `server.go`

Edit the produced `server.go` `NewHandler` to construct the entity
repository and register its routes — follow the pattern of
`RegisterWhoamiRoute` already present.

### Step 6 — deploy artifacts

This skill produces the service's source only; it stops one step short of
something `erun deploy` can install. Apply the `erun-blueprint-service`
skill against the module produced above to add the missing
`<tenant>-devops/docker/<module>/Dockerfile` and
`<tenant>-devops/k8s/<module>/` chart — that skill's Dockerfile builder
stage is a Go skeleton already, so point it at `cmd/<module>` and
`ERUN_API_PORT`'s default (`17033`, `server.go`) as the container port and
health-check path (`GET /healthz`, already implemented). Do this before
declaring the API "done": a scaffold that stops at `go build ./...` looks
finished but has nothing `erun build`/`erun deploy` can find.

### Step 7 — validate

```sh
cd "${target_dir}/${module}"
go mod tidy
go build ./...
go test ./...
```

Run a smoke test against a local PostgreSQL instance configured with
the matching `erun-backend-db`-shaped schema. Confirm:

- `GET /healthz` returns 204 without a token.
- `GET /v1/whoami` with no token returns 401.
- `GET /v1/whoami` with a valid OIDC token returns the resolved user.
- A second valid request from a different `(iss, sub)` on a freshly
  bootstrapped database is rejected (no implicit user creation).

## Maintenance, repair & upgrade

This skill owns the service after first scaffold too. When the target
already carries an erun-backend-api-shaped module, do not stop — enter
maintenance mode and reconcile it in place. Idempotent, in-place,
preview-first: safe to re-run, edit in place, and show the diff/plan
(files to add, layers to restore, version pins old→new) before writing.
Touch only version pins and blueprint gaps — never genuine project
content.

### Detect

- Existing artifacts (`go.mod`, `server.go`,
  `internal/repository/tx.go`) mean maintain, not scaffold.
- Read the current layout, the Go module path, the `go` toolchain line,
  and the service's dependency `require` pins in `go.mod`.

### Repair (reconcile to the current blueprint)

- Re-align structural drift against `erun-backend-api`'s current shape:
  a missing layer (`model` / `repository` / `service` / `routes`),
  absent OIDC/authentication, authorization, or audit middleware,
  missing tenant-from-issuer resolution, a dropped
  `TxManager.WithTx` security-context wiring (RLS `SET LOCAL ROLE` /
  `erun.tenant_id`), a missing identity-resolution cache, or a missing
  audit hook.
- Fill each gap with the blueprint's shape from `templates/`. Never
  clobber the project's own domain entities, handlers, or business
  logic — restore the missing plumbing around them.
- Add missing scaffolding files (`.gitignore` entries, module
  metadata) without rewriting project content.
- A module with source but no `<tenant>-devops/docker/<module>/Dockerfile` or
  `<tenant>-devops/k8s/<module>/` chart is a gap too — apply
  `erun-blueprint-service` (Step 6) to close it during the same maintenance
  pass rather than leaving the service undeployable.

### Upgrade (refresh to the current blueprint)

- This is a **standalone** Go module — it carries no erun/`erun-common`
  dependency and no `VERSION` marker, so there is no erun version to pin
  it to. "Upgrade" means realigning it to `erun-backend-api`'s *current*
  blueprint shape (see Repair) and refreshing the service's **own**
  dependency pins.
- Bump the `require` pins the service carries (`go-oidc`, `bun`, `uuid`,
  `oauth2`, …) and the `go` toolchain line to current, and adopt any
  structural change the blueprint has made since scaffold.
- Refresh derived state: `go mod tidy`, then `go build ./...` and
  `go test ./...` to confirm the bump is clean.

### Clean up

- Remove only scaffolding this blueprint previously emitted but no longer
  does — a renamed or merged generated file (e.g. an old `oidc.go`/`auth.go`
  split the blueprint has since combined) — after previewing the deletion.
  Never delete the project's own domain entities, handlers, or business
  logic; when a file mixes generated plumbing with the project's code, leave
  it and flag the drift rather than removing it.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| Target dir already contains a `go.mod` | Do not stop. Enter maintenance mode (see "Maintenance, repair & upgrade") — reconcile against the blueprint and re-pin versions in place. Do not clobber the project's own content. |
| User-supplied entity name is singular plural-form ambiguous | Ask for both singular (`invoice`) and plural (`invoices`) explicitly. Do not guess. |
| User-supplied route prefix doesn't start with `/v1/` | Surface the convention and ask the user to confirm or change. |
| OIDC issuer list is empty | Stop. Ask for at least one issuer; no point producing an unauthenticated API. |
| `go build` fails after generation | Surface the compiler output. Most common cause is module path mismatch — confirm `__MODULE_PATH__` substitution. |
| The matching database doesn't have the bootstrap tables | Surface the missing tables and offer to run `erun-blueprint-rls-db` first. |

## Important

- Give the repo root agent guidance. If the repository root has no
  `AGENTS.md`/`CLAUDE.md`, also apply the `erun-blueprint-agents` skill so any
  agent — or human — landing in the repo gets erun-environment orientation.
- This skill's own output is not deployable by itself. Always follow Step 6
  and apply `erun-blueprint-service` for the Dockerfile and chart, or
  confirm with the user that equivalent deploy artifacts already exist.
- Do not let CLI/MCP modules import this API directly. Shared
  contracts go in a separate transport-neutral library, not in the API
  module.
- Do not put workflow names on repositories (`UpdateStatus`,
  `AdvanceMergeQueue`). Repository = CRUD; service = workflow.
- Do not check role names in route handlers. Authorization middleware
  computes effective permissions from `role_permissions`.
- Do not generate or accept externally visible IDs in application
  code. Database defaults own them.
- Do not write audit events from individual route handlers. Middleware
  owns routine audit.
- Do not bypass `TxManager.WithTx`. Direct `db.Exec` calls skip the
  security context setup and break RLS.
- This skill encodes ERun's accumulated best practices for
  multi-tenant HTTP APIs; do not negotiate them away to match a user's
  existing preferences. If the user's existing project conflicts with
  the blueprint, surface the conflict; do not silently relax the
  convention.
