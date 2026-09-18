# AGENTS.md

Module-specific guidance for `erun-backend-db`. Follow the repository root and `erun-backend/AGENTS.md` first.

## Module Role

- `erun-backend-db` is the Atlas-managed database project for hosted ERun backend state.
- The default OLTP database is PostgreSQL 18 or newer.
- Do not add SQLite migrations, schema files, repository branches, or local fallback behavior.
- Store audit events in PostgreSQL with the rest of the hosted backend schema. Do not reintroduce SQLite audit storage as a simple-deployment fallback.

## Atlas Workflow

- Store Atlas configuration in `atlas.hcl`.
- Store default OLTP migrations in `migrations/default/`.
- Store declarative target schema files in `schema/` when generating migrations.
- Generate schema changes through Atlas rather than hand-maintaining API startup DDL.
- Validate OLTP migrations against the default Atlas environment.
- Keep audit-event schema in PostgreSQL schema files when audit persistence changes.

### Schema/Migration Drift Check (#2022)

- Declarative `atlas.hcl` sources and replayed migrations must produce the same
  schema. Update both, including grants, explicit constraint names, triggers,
  functions, and source dependency order.
- The pinned Atlas diff requires login for this function-bearing schema. Use
  `make test-schema-drift`: build both states in real PostgreSQL, obtain source
  order from atlas.hcl, and compare SQL introspection ordered by object name,
  not physical column order.
- Run this Docker/Atlas-dependent check before merging schema, migration, or
  atlas.hcl changes; it is not covered by the bare `make check` environment.

## Schema Layout

- Organize declarative schema by database object type under `schema/`.
- Put one table definition per file in `schema/tables/<table>.sql`.
- Put indexes in `schema/indexes/<table>.sql` when they are owned by one table.
- Put cross-table or specialized objects in their own object-type folders, such as `schema/views/`, `schema/triggers/`, or `schema/policies/`, when those objects are introduced.
- Keep table files focused on the table contract: columns, primary key, foreign keys, and table-level constraints.
- Keep secondary indexes separate from table files unless the index is part of a table-level uniqueness contract that is clearer beside the table definition.
- Put trigger files under `schema/triggers/`.
- Put row-level security policy files under `schema/rls/<table>.sql`.
- Put default-database foreign-key files that are clearer outside table definitions under `schema/fks/`.
- Keep primary-key defaults in table definitions, not in trigger files.
- Keep `atlas.hcl` as the ordered list of schema source files. Add new schema files there intentionally so review shows the source ordering Atlas uses.
- Do not organize migrations per table. Keep versioned migrations in one chronological stream per dialect so database upgrades preserve the real cross-table change order.
- Prefer splitting by stable ownership. Do not create vague catch-all schema files such as `common.sql` or `misc.sql`.

## PostgreSQL Types

- Use `UUID` for externally visible IDs.
- Generate UUIDv7 surrogate primary keys with column defaults that call native `uuidv7()`.
- Use `TIMESTAMPTZ` for timestamps. Timestamp columns are populated by PostgreSQL triggers.
- Native database features such as RLS, `TIMESTAMPTZ`, and `uuidv7()` are allowed in the default OLTP schema when they are the clearest way to enforce the backend contract.
- Audit tables use PostgreSQL types and constraints.

## Row-Level Security

- PostgreSQL row-level security is mandatory for tenant-owned tables in hosted deployments.
- Keep RLS definitions in schema files under `schema/rls/` and include them in the default Atlas env.
- Keep RLS statements in `migrations/default/`.
- Enable and force RLS on every tenant-owned PostgreSQL table with `ALTER TABLE <table> ENABLE ROW LEVEL SECURITY` and `ALTER TABLE <table> FORCE ROW LEVEL SECURITY`.
- RLS policies must scope rows by `tenant_id` using a database session setting named `erun.tenant_id`.
- Use `erun_current_tenant_id()` in tenant-scoped policies so a missing tenant setting denies access instead of matching rows.
- Tenant-owned tables should default `tenant_id` from `erun_current_tenant_id()` so normal application inserts omit caller-provided tenant IDs and let the transaction security context populate ownership.
- `erun_current_user_id()` is the sibling of `erun_current_tenant_id()`, reading the `erun.user_id` session setting the same way. Default a user-ownership column (such as `reviews.author_user_id`) to it rather than accepting the owner from the caller — a caller-supplied owner column is an impersonation surface the same way a caller-supplied `tenant_id` would be.
- Define policies with both `USING` and `WITH CHECK` so reads, updates, deletes, and writes all enforce the tenant boundary.
- Keep normal tenant and operations access in separate PostgreSQL roles and separate RLS policies. Do not put an `OR` branch for operations access inside tenant-scoped policies.
- PostgreSQL role `erun_tenant` is for normal tenant-scoped access. PostgreSQL role `erun_operations` is for operations-tenant access across tenant-owned rows.
- The API database login role must be allowed to `SET ROLE erun_tenant` and `SET ROLE erun_operations`, for example by granting those roles to the application login during deployment.
- Operations tenants are allowed through `erun_operations` RLS policies for tenant-owned rows across tenants. Application authorization still controls which operations tenant users have broad permissions.
- API and worker code must set `SET LOCAL ROLE`, then `erun.tenant_id`, on the PostgreSQL transaction before running tenant-owned SQL. This is database enforcement setup, not application-side filtering.
- API and worker code must set `erun.user_id` on PostgreSQL transactions that update user-owned row state, such as closing comments.
- Root tenant resolution tables such as `tenants` and `tenant_issuers` are not tenant-owned in the same way as operational tables. Access to issuer lookup must be handled through tightly scoped database roles, grants, or future security-definer functions before tenant context exists.
- Do not add new tenant-owned tables without adding the matching RLS policy file and including it in the default Atlas env source list.

## Naming And Keys

- Use plural snake_case table names, such as `tenants`, `tenant_issuers`, `users`, `user_external_ids`, `environments`, and `deployments`.
- Use explicit entity-prefixed primary key column names instead of a generic `id` on root/domain tables.
- Name root table primary keys as `<entity>_id`, for example `tenants.tenant_id`, `environments.environment_id`, and `deployments.deployment_id`.
- Use the same column name for foreign keys where practical. Tenant-owned tables should use `tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id() REFERENCES tenants(tenant_id)`.
- Use generic `id` only for small private join tables or append-only internal records when the ID is never part of an API contract and no clearer entity name exists.
- Use UUIDv7 for externally visible primary keys and foreign keys that point to externally visible primary keys.
- Use `DEFAULT uuidv7()` on UUID primary-key columns that own externally visible identities.
- Give tenant-owned tables a `tenant_id` column even when another foreign key could imply the tenant. Tenant scoping should be directly visible in table definitions, query predicates, and indexes.
- Name indexes as `<table>_<column_list>_idx`, such as `user_external_ids_tenant_user_idx`.
- Scope tenant-owned natural-key uniqueness by `tenant_id`, for example `UNIQUE (tenant_id, name)` instead of `UNIQUE (name)`.
- Use `created_at TIMESTAMPTZ` and `updated_at TIMESTAMPTZ` on mutable domain tables.
- Keep column names stable and domain-specific. Avoid vague columns such as `value`, `data`, `ref`, or `type` unless the table owns a deliberately generic key-value contract.

## Primary Key Defaults

- Use column defaults to populate UUIDv7 surrogate primary keys for domain tables that own externally visible identities.
- UUIDv7 defaults currently apply to `tenants.tenant_id`, `users.user_id`, `roles.role_id`, `role_permissions.role_permission_id`, `reviews.review_id`, `builds.build_id`, `comments.comment_id`, and `audit_events.audit_event_id`.
- Define those columns inline as `<entity>_id UUID PRIMARY KEY DEFAULT uuidv7()`.
- Do not add UUID defaults for natural keys such as `tenant_issuers.issuer` or composite association keys such as `user_external_ids (tenant_id, issuer, external_id)`.
- UUID primary-key defaults must call native `uuidv7()`. Do not add a custom UUIDv7 implementation.
- Private integer queue/order keys should use native identity columns, such as `GENERATED BY DEFAULT AS IDENTITY`.
- Caller-provided UUIDv7 primary keys are allowed only for direct database imports, deterministic fixtures, and data repair. Normal application inserts should omit default-owned primary keys.
- When adding a new default-owned primary key, validate by inserting without the key.

## Timestamp Triggers

- Use database triggers to populate `created_at` and `updated_at`.
- Timestamp triggers belong in `schema/triggers/` and `migrations/default/`.
- Include trigger files in the default Atlas env source list immediately after table and index files, before RLS files.
- Keep trigger names deterministic: `<table>_set_timestamps`.
- Keep PostgreSQL timestamp behavior in one shared trigger function named `erun_set_timestamps()` unless a table has a real exception.
- Inserts may omit `created_at` and `updated_at`; triggers must populate both.
- Updates must preserve `created_at` and refresh `updated_at`.
- PostgreSQL triggers should be `BEFORE INSERT OR UPDATE` so rows are stored with timestamps in one write.
- Preserve caller-provided `created_at` on updates. `created_at` is creation identity, not mutable metadata.
- Caller-provided timestamps are allowed for imports and deterministic tests, but normal application inserts should omit both timestamp columns.
- Do not add application-side timestamp fallback as the primary behavior. Application code may pass explicit timestamps for tests or imports, but database triggers own the default lifecycle.
- When adding a mutable table with timestamp columns, add PostgreSQL trigger coverage.
- Validate timestamp trigger changes by inserting without timestamps and updating at least one row in PostgreSQL.

## Natural Keys And Mappings

- Prefer natural keys when the source value is globally stable, already externally defined, and is the exact lookup key used by the workflow.
- Do not add surrogate IDs to mapping tables when a natural key already exists and is stable.
- `tenant_issuers` does not use `issuer` as a global primary key: a shared (org-scoped) issuer maps to many tenants. Its identity is `UNIQUE (tenant_id, issuer)` (a tenant maps an issuer once), and resolution uses `UNIQUE NULLS NOT DISTINCT (issuer, org_field_value)`. The per-issuer org-scoping mode lives once on `issuers.org_field_key`, and `issuers.issuer` is the globally unique issuer registry key.
- Mapping tables may still include `tenant_id` or other foreign keys for traversal and integrity, but those foreign keys should not replace the natural lookup key.
- Add composite uniqueness when another table needs to prove that two columns belong together. `tenant_issuers` keeps `UNIQUE (tenant_id, issuer)` so `user_external_ids` can foreign-key `(tenant_id, issuer)`.
- Use UUIDv7 surrogate primary keys only for domain records that need their own externally visible identity, such as `tenants.tenant_id` or `users.user_id`.
- Do not use UUID primary keys for purely internal association rows unless there is a current API, audit, or lifecycle requirement to address that row directly.
- Register each OIDC issuer once in `issuers` (the globally unique issuer key). It may resolve to one tenant (single-tenant, NULL `org_field_value`) or many (org-scoped, one `tenant_issuers` row per org value). Multiple distinct issuers may still map to the same tenant.

## Multi-Tenant Database Plan

- Use a shared database with tenant-scoped rows by default, not one database per tenant.
- The `tenants` table is the root tenant registry. It stores tenant identity and tenant type without assuming a single identity provider issuer.
- `tenants.type` must be one of `OPERATIONS` or `COMPANY` and defaults to `COMPANY`.
- The `issuers` table holds each OIDC issuer once with its org-scoping mode: `org_field_key` NULL means a single-tenant issuer (the common case — BYO/external IdPs like cloud workload identity or a tenant's own OIDC); a set `org_field_key` names the token claim carrying the org for a shared multi-tenant issuer (e.g. a hosted Zitadel).
- The `tenant_issuers` table maps `(issuer, org_field_value) → tenant`. An issuer is **not** globally unique to one tenant: a single-tenant issuer maps to exactly one tenant (NULL `org_field_value`), and an org-scoped issuer maps to many tenants (one row per org value). `UNIQUE NULLS NOT DISTINCT (issuer, org_field_value)` keeps the resolution key unambiguous; `tenant_issuers.issuer` foreign-keys `issuers(issuer)`. Multiple distinct issuers may still map to the same tenant.
- The `users` table stores tenant-owned users with `user_id` as the UUIDv7 externally visible user identity.
- The `user_external_ids` table maps multiple external identity-provider subjects to one user.
- User external IDs must be unique per tenant with `PRIMARY KEY (tenant_id, issuer, external_id)`.
- `user_external_ids` must foreign-key `(tenant_id, user_id)` to `users` and `(tenant_id, issuer)` to `tenant_issuers` so external IDs cannot cross tenant or issuer boundaries.
- The `roles` table stores tenant-owned authorization roles with tenant-scoped role names.
- The `user_roles` table assigns multiple roles to users with `PRIMARY KEY (tenant_id, user_id, role_id)`.
- The `role_permissions` table stores role-owned permissions. A permission is either an exact API method/path pair or a regex API method/path pattern pair.
- Exact `role_permissions.api_method` values must be one of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, or `HEAD`.
- Regex permissions must use `api_method_pattern` and `api_path_pattern`, and should be anchored with `^` and `$` unless partial matching is explicitly intended.
- Keep `ReadAll` and `WriteAll` as predefined tenant roles. `ReadAll` grants all read-style methods across all API paths, and `WriteAll` grants all write-style methods across all API paths.
- Role permissions must be unique per tenant and role for exact values with `UNIQUE (tenant_id, role_id, api_method, api_path)` and for pattern values with `UNIQUE (tenant_id, role_id, api_method_pattern, api_path_pattern)`.
- A user's effective permissions are calculated as the distinct union of permissions for all roles assigned to that user within the same tenant.
- Authorization queries must join through `user_roles` and `role_permissions` scoped by `tenant_id`; do not calculate permissions from role names alone.
- If the database has no tenants, the first authenticated identity may bootstrap the system by creating an `OPERATIONS` tenant, mapping the token issuer to that tenant, creating the first user, and assigning both `ReadAll` and `WriteAll`.
- Do not bootstrap another operations tenant once any tenant exists. Unknown issuers remain unauthorized; first-user enrollment for an existing zero-user tenant follows the explicit API bootstrap contract, not unrestricted subject creation.
- The `reviews` table stores tenant-owned review records with tenant-scoped names, non-empty `target_branch`, and non-empty `source_branch`.
- Review `name` is the squash merge message.
- `reviews.author_user_id` records who opened the review, defaulted from `erun_current_user_id()` and foreign-keyed to `users (tenant_id, user_id)`. It is set once, at creation, and never reassigned.
- The `review_reviewers` table assigns reviewers to a review with `PRIMARY KEY (tenant_id, review_id, user_id)`, tenant-scoped FKs to `reviews` and `users`. Many reviewers per review is the default shape; assigning a reviewer does not gate any status transition on its own.
- `reviews.status` must be one of `OPEN`, `CLOSED`, `FAILED`, `READY`, `MERGE`, or `MERGED`.
- Reviews track `last_failed_build_id`, `last_ready_build_id`, and `last_merged_build_id`. When a review status is `FAILED`, `READY`, `MERGE`, or `MERGED`, the matching last-build column must be populated.
- At most one review with status not in `MERGED`/`CLOSED` may exist per `(tenant_id, source_branch, target_branch)`, enforced by a partial unique index (`reviews_tenant_live_source_target_idx`) rather than application logic, so a second live proposal of the same change is refused at insert time. A recycled branch name may have many terminal reviews; retention of CLOSED history is governed below, while MERGED history remains exempt.
- The `review_merge_queue` table stores per-target-branch queue membership. Queue order is the ascending internal integer `review_merge_queue_id` surrogate key, not a mutable position column.
- Move reviews through the queue by deleting and inserting `review_merge_queue` rows. Requeue a review by deleting any old queue row, setting the review back to `READY`, and inserting a new row so it sorts at the end.
- `READY` reviews may appear in `review_merge_queue`. The active `MERGE` review must be removed from `review_merge_queue` when it is promoted, so the queue table contains only waiting reviews.
- `CLOSED` reviews must not appear in the merge queue.
- Build records identify a review or environment; GATE requires a review and NULL version, RECORDED requires a non-empty version. A failed GATE requires failure detail. Enforce the kind/version pairing in SQL, not only application code; keep `TestGateBuildContractAllowsNoVersionAndRequiresFailureDetail`.
- A successful build moves an `OPEN` or `FAILED` review into the target branch merge queue as `READY`; if there is no active `MERGE` review for that target branch, the next queued review may be promoted to `MERGE`.
- A failed build for a queued or merging review moves it to `FAILED` and removes it from the merge queue.
- If a `MERGE` review misses its merge window without failing, move it back to `READY` at the end of the same target branch queue.
- The `gate_runs` table stores the first-class record of one attempt to gate a prospective merge — independent of whether an erun review exists for the change. Reviews are one producer (a review-driven merge reports a gate run alongside its GATE build); a repository whose changes arrive as GitHub pull requests, with no erun review at all, is the other. `status` is one of `RUNNING`, `PASSED`, `FAILED`, or `INCONCLUSIVE` — a wrapper timing out or a run interrupted by an environment fault must report `INCONCLUSIVE`, never `FAILED`, since `FAILED` asserts a real verdict was reached. A `FAILED` row must carry `failing_step` (which gate step produced the red verdict); `log_ref` is an optional pointer to where to read it. `merge_commit` is NULL only when the run failed before that commit existed at all (a squash conflict). `review_id` is nullable and, when set, foreign-keys `(tenant_id, review_id)` to `reviews`.
- Audit events are stored in PostgreSQL.
- Every tenant-owned PostgreSQL OLTP table must include `tenant_id`, enforce tenant-scoped uniqueness with composite unique indexes, and have PostgreSQL RLS.
- API request handling must resolve tenant from the bearer token issuer before running tenant-owned queries.
- Persistence code must require authenticated tenant identity in transaction context, then omit `tenant_id` from normal application inserts so the database default uses `erun_current_tenant_id()`.
- Use UUIDv7 values for externally visible IDs. Keep database primary keys and API IDs aligned unless a later migration introduces private surrogate keys for a measured reason.
- Store timestamps in UTC. Database triggers own default timestamp population.
- Add foreign keys from tenant-owned tables to `tenants(tenant_id)`.

## Initial Schema Direction

Object definitions live in `schema/tables/`; the multi-tenant and review
constraints above apply to new tables too. Do not maintain a second field-by-field
schema inventory in this guide.

## Audit Events

- Audit events track authenticated API, MCP, and CLI activity.
- Store audit events in PostgreSQL for normal deployments.
- Required common audit fields are `tenant_id`, `erun_user_id`, `external_user_id`, `external_issuer_id`, `type`, and `created_at`.
- `type` must be one of `API`, `MCP`, or `CLI`.
- `external_issuer_id` stores the OIDC `iss` value that mapped to the tenant.
- `external_org_id` stores the org/resource-owner claim value (for org-scoped issuers) that, together with `iss`, resolved the tenant; NULL for single-tenant issuers.
- `external_user_id` stores the external subject/user ID presented by the identity provider.
- `erun_user_id` stores the internal ERun user ID resolved from the external identity.
- API audit events must set `api_method` and `api_path`. `api_method` must be one of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, or `HEAD`.
- `api_path` must use the same canonical route template stored in `role_permissions.api_path`, such as `/v1/reviews/{review_id}` rather than a concrete request URL with IDs or query strings.
- CLI audit events must set `cli_command` and may set `cli_parameters`.
- MCP audit events must set `mcp_tool` and may set `mcp_tool_parameters`.
- API audit events may set `api_parameters` when the action itself requires recording a caller-supplied justification, such as a merge-queue gate override's reason; ordinary API audit events leave it null.
- Store CLI and MCP parameters as serialized text, preferably compact JSON when the caller has structured input.
- Audit events are append-only. Do not update or delete audit rows as part of normal application behavior.
- PostgreSQL audit events should be indexed for tenant/time/user/API access patterns.
- `audit_events` records authenticated, single-tenant activity only — a system-initiated action with no caller identity behind it (e.g. the scheduled retention sweep) does not fit here and belongs in its own table instead. See § "Retention Mechanism"'s `retention_runs` bullets for the reasoning and the self-referential boundary this avoids.

## Review Comments

- Comments belong to reviews through `(tenant_id, review_id)`.
- Comments must include `commit_id`, non-empty `file_path`, and positive `line`. A comment's address is the full `(commit_id, file_path, line)` tuple, not `(commit_id, line)` alone — two files can share a line number in the same commit.
- Comments must include non-empty, non-whitespace-only `body`, bounded to 8 KiB (`octet_length(body) <= 8192`).
- `comments.status` must be one of `OPEN` or `CLOSED`.
- Every comment, root or child, must have `creator_user_id`. Every reply records its own author; the root's creator does not stand in for a reply's author.
- Root comments have `parent_comment_id IS NULL`.
- Comments must not reference themselves as parents.
- Comment thread identity fields are immutable after insert: `comment_id`, `tenant_id`, `review_id`, `creator_user_id`, `parent_comment_id`, `commit_id`, `file_path`, `line`, and `body` (there is no edit endpoint in this increment).
- There can be only one root comment per tenant, review, commit, file_path, and line.
- Child comments must reference the root comment for the same tenant, review, commit, file_path, and line; a reply whose file disagrees with its parent's is refused.
- Only a thread's root comment may have its `status` updated; a reply's own status is not separately settable.
- PostgreSQL must enforce that only the root comment's own creator can update that root's status by comparing `creator_user_id` to transaction setting `erun.user_id`. There is no per-review "any write-access actor" exception at the database layer — the database cannot see role-based write permissions, so that documented allowance was narrowed to "the root author" to match what the trigger can actually enforce.
- Do not calculate comment-thread validity in API code as the primary guard. Keep the review/commit/file_path/line and parent-child invariants in database triggers.

## Retention For High-Frequency Tables (#1956, design recorded — not yet implemented)

- Proposed: age OR count bound, initially 30 days / 2,000 rows. Key build caps by
  tenant/environment now that `builds.environment_id` exists; define handling
  for unattached/null environments when implementing. Gate-run keying must follow
  its actual schema. No policy file implements this proposal yet.
- Exempt builds referenced by reviews' last-build pointers or `releases.build_id`
  with explicit NOT EXISTS guards. MERGED reviews must retain their pinned build.
  Do not weaken FKs to make pruning succeed.
- Use the existing scheduled retention runner, not lazy pruning on reads.
  Explicitly filter tenant, age, and rank under the cross-tenant operations role.
  Builds/gate_runs already permit DELETE; audit/usage do not.
- Bounds are starting estimates, not measured production obligations. Tune from
  actual per-tenant counts after rollout; no speculative rollup is required.
  The policy is additive SQL under `retention/`, not a new mechanism.

## Retention For Append-Only Audit/Usage Tables (#1959, design recorded — a compliance decision blocks implementation)

- No deletion until the operator establishes audit/compliance retention floors
  (including per-tenant requirements), archival requirements, and usage/billing
  dispute windows. Absence of a known obligation is not permission to guess one.
- Keep application roles unable to delete these rows. Any future exception needs
  a dedicated retention role/login, inaccessible to the API login, with explicit
  grants/policies and separately reviewed connection handling.
- Prefer archive-before-delete; destination/format and the archival mechanism are
  unresolved. Preserve append-only guarantees until that design is approved.
- Audit volume follows authorized API calls; classifying existing calls as MCP
  does not add events. Auditing previously unaudited tools is a separate volume
  and retention decision. Usage volume follows lifecycle transitions. Measure
  each rather than extrapolating historical merge counts as production telemetry.

## Retention For The #1968 Six-Table Sweep

The following policies are implemented; the two proposals above are not.
Pruning can irreversibly remove provenance even when no live operation breaks.
Do not silently change windows or exemptions while refactoring.

### Retention Mechanism

- One `retention/*.sql` file per policy, executed by the existing devops runner
  in name order in one psql session. Use the shared daily CronJob, not a new
  scheduler. `retention.enabled=false` and `retention.dryRun=true` are independent
  defaults; enabling the job must not itself enable deletion.
- Preserve explicit false Helm values with hasKey-aware resolution. Honor the
  backend-db image override and retention entrypoint.
- Report eligible counts before deletion. Use the same predicate for report,
  delete, and durable result; each policy's mutations are transactional.
- Hold a session advisory lock across the entire sweep; refuse a concurrent
  manual or scheduled run. CronJob Forbid alone is insufficient.
- Every policy records dry/real outcomes per table in `retention_runs`,
  including eligible/deleted counts; dry runs record zero deletions.
  This is operations-only, platform-wide data, not tenant audit_events:
  a scheduled sweep has no authenticated actor to invent.
- Do not let retention delete its own evidence before it can be read.
  `retention_runs` has no pruning policy today; any future bound must preserve
  evidence beyond the run that would prune it.
- Keep explicit tenant predicates under operations access. A future audit-specific
  login must not widen this shared connection's privileges.

### Review Comments And Releases (#1970, implemented)

`retention/comments_releases.sql`:

- CLOSED root threads only: 30 days from updated_at OR 5,000 per tenant.
  Delete the whole reply set with its root; never independently prune replies
  or OPEN roots.
- Releases: 180 days from created_at OR 1,000 per tenant.
  Deleting a release row weakens per-commit database idempotency even though the
  external tag/artifact remains; retain that consequence in policy review.

### Reviews (#1971, implemented — scope narrowed to the unblocked case)

`retention/reviews.sql`:

- CLOSED only: 90 days from updated_at OR 2,000 per tenant. Live and MERGED
  reviews are exempt; MERGED-history retention remains an operator decision.
- Guard surviving comments, releases, builds (including unpinned builds),
  gate_runs, and queue membership. Remove reviewer assignments only together
  with their deleted review.
- Eligible CLOSED reviews may clear stale failed/ready pins so future build
  retention can progress. Never null a MERGED review's required build pin.
- Until build/gate-run retention exists, referenced CLOSED reviews may remain
  indefinitely. That limits effectiveness, not referential safety.
- Any future MERGED policy must handle the review/pinned-build FK cycle and
  release backreferences, not assume null-then-delete can satisfy the checks.

### AI Sessions (#1972, implemented)

`retention/ai_sessions.sql`:

- Exit-event rows only: 14 days from occurred_at OR 500 per tenant/environment.
  Keep non-exit states and the per-environment partition.
- These are last-state upserts, not turn history; environment deletion cascades.
  Keep DELETE grants consistent between schema and migrations.
- Live producer/volume verification remains a disclosed gap from the original
  design; retune estimates only from real counts, not assumed session traffic.

### Invites And Invite Requests (#1973, implemented)

`retention/invites_invite_requests.sql`:

- Decided requests: 180 days from updated_at OR 10,000 platform-wide.
  PENDING is exempt. Requests are unauthenticated/global; do not invent tenant
  keying from their free-text tenant name.
- Consumed invites: 365 days from consumed_at; unused expired invites: 30 days
  from expires_at. Each population has a separately ranked 10,000-per-tenant cap.
  Live invites are exempt.
- Prune requests first, then invites in the same transaction. Guard any invite
  still referenced by minted_invite_id, including a PENDING request.
- Admission provenance and decline reasons are not fully reconstructible from
  audit_events. Bounds affect that evidence, not only storage.

## Validation

- Schema changes run `make test-schema-drift` and validate actual default-key,
  timestamp, RLS, and user-owned update behavior in PostgreSQL.
- Retention changes run `make test-retention` and `make test-retention-grants`.
  Assert exact surviving rows, each bound/exemption, dry/real recording, and
  concurrent-run refusal. Exercise actual INSERT/DELETE under operations and
  denied DELETE on audit/usage; catalog privileges alone are insufficient.
- These checks require Docker/Atlas as documented by devops. Guidance-only
  changes follow root consistency validation and do not run a destructive sweep.
