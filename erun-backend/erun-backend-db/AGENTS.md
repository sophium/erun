# AGENTS.md

Module-specific guidance for `erun-backend-db`. Follow the repository root and `erun-backend/AGENTS.md` first.

## Module Role

- `erun-backend-db` is the Atlas-managed database project for hosted ERun backend state.
- The default OLTP database is PostgreSQL 18 or newer.
- Do not add SQLite migrations, schema files, repository branches, or local fallback behavior.
- Store audit events in ClickHouse for normal deployments. Do not reintroduce SQLite audit storage as a simple-deployment fallback.

## Atlas Workflow

- Store Atlas configuration in `atlas.hcl`.
- Store default OLTP migrations in `migrations/default/`.
- Store ClickHouse migrations in `migrations/clickhouse/`.
- Store declarative target schema files in `schema/` when generating migrations.
- Generate schema changes through Atlas rather than hand-maintaining API startup DDL.
- Validate OLTP migrations against the default Atlas environment.
- Keep audit-event schema in ClickHouse-native files when audit persistence changes.

## Schema Layout

- Organize declarative schema by database object type under `schema/`.
- Put one table definition per file in `schema/tables/<table>.sql`.
- Put indexes in `schema/indexes/<table>.sql` when they are owned by one table.
- Put cross-table or specialized objects in their own object-type folders, such as `schema/views/`, `schema/triggers/`, or `schema/policies/`, when those objects are introduced.
- Keep table files focused on the table contract: columns, primary key, foreign keys, and table-level constraints.
- Keep secondary indexes separate from table files unless the index is part of a table-level uniqueness contract that is clearer beside the table definition.
- Put ClickHouse-only table files under `schema/clickhouse/tables/`.
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
- For ClickHouse audit tables, use ClickHouse-native types and engines rather than forcing PostgreSQL-compatible DDL.

## Row-Level Security

- PostgreSQL row-level security is mandatory for tenant-owned tables in hosted deployments.
- Keep RLS definitions in schema files under `schema/rls/` and include them in the default Atlas env.
- Keep RLS statements in `migrations/default/`.
- Enable and force RLS on every tenant-owned PostgreSQL table with `ALTER TABLE <table> ENABLE ROW LEVEL SECURITY` and `ALTER TABLE <table> FORCE ROW LEVEL SECURITY`.
- RLS policies must scope rows by `tenant_id` using a database session setting named `erun.tenant_id`.
- Use `erun_current_tenant_id()` in tenant-scoped policies so a missing tenant setting denies access instead of matching rows.
- Tenant-owned tables should default `tenant_id` from `erun_current_tenant_id()` so normal application inserts omit caller-provided tenant IDs and let the transaction security context populate ownership.
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
- UUIDv7 defaults currently apply to `tenants.tenant_id`, `users.user_id`, `roles.role_id`, `role_permissions.role_permission_id`, `reviews.review_id`, `builds.build_id`, and `comments.comment_id`.
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
- `tenant_issuers` uses `issuer` as its primary key because the OIDC issuer is the authenticated lookup key and must be globally unique.
- Mapping tables may still include `tenant_id` or other foreign keys for traversal and integrity, but those foreign keys should not replace the natural lookup key.
- Add composite uniqueness when another table needs to prove that two columns belong together. `tenant_issuers` keeps `UNIQUE (tenant_id, issuer)` so `user_external_ids` can foreign-key `(tenant_id, issuer)`.
- Use UUIDv7 surrogate primary keys only for domain records that need their own externally visible identity, such as `tenants.tenant_id` or `users.user_id`.
- Do not use UUID primary keys for purely internal association rows unless there is a current API, audit, or lifecycle requirement to address that row directly.
- Keep identity-provider issuers globally unique in `tenant_issuers.issuer`, while allowing multiple issuer rows to reference the same `tenant_id`.

## Multi-Tenant Database Plan

- Use a shared database with tenant-scoped rows by default, not one database per tenant.
- The `tenants` table is the root tenant registry. It stores tenant identity and tenant type without assuming a single identity provider issuer.
- `tenants.type` must be one of `OPERATIONS` or `COMPANY` and defaults to `COMPANY`.
- The `tenant_issuers` table maps OIDC issuers (`iss`) to tenants. Multiple issuers may map to the same tenant, but each issuer must be globally unique.
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
- Do not bootstrap another operations tenant once any tenant row exists. After the first tenant exists, unknown issuers and subjects must remain unauthorized until explicitly configured.
- The `reviews` table stores tenant-owned review records with tenant-scoped names, non-empty `target_branch`, and non-empty `source_branch`.
- Review `name` is the squash merge message.
- `reviews.status` must be one of `OPEN`, `CLOSED`, `FAILED`, `READY`, `MERGE`, or `MERGED`.
- Reviews track `last_failed_build_id`, `last_ready_build_id`, and `last_merged_build_id`. When a review status is `FAILED`, `READY`, `MERGE`, or `MERGED`, the matching last-build column must be populated.
- The `review_merge_queue` table stores per-target-branch queue membership. Queue order is the ascending internal integer `review_merge_queue_id` surrogate key, not a mutable position column.
- Move reviews through the queue by deleting and inserting `review_merge_queue` rows. Requeue a review by deleting any old queue row, setting the review back to `READY`, and inserting a new row so it sorts at the end.
- `READY` reviews may appear in `review_merge_queue`. The active `MERGE` review must be removed from `review_merge_queue` when it is promoted, so the queue table contains only waiting reviews.
- `CLOSED` reviews must not appear in the merge queue.
- The `builds` table stores tenant-owned review build records. Each build belongs to one review and stores whether it was successful, the commit ID it ran on, and the produced version.
- A successful build moves an `OPEN` or `FAILED` review into the target branch merge queue as `READY`; if there is no active `MERGE` review for that target branch, the next queued review may be promoted to `MERGE`.
- A failed build for a queued or merging review moves it to `FAILED` and removes it from the merge queue.
- If a `MERGE` review misses its merge window without failing, move it back to `READY` at the end of the same target branch queue.
- Audit events are stored in ClickHouse when audit persistence is enabled.
- Every tenant-owned PostgreSQL OLTP table must include `tenant_id`, enforce tenant-scoped uniqueness with composite unique indexes, and have PostgreSQL RLS.
- API request handling must resolve tenant from the bearer token issuer before running tenant-owned queries.
- Persistence code must require authenticated tenant identity in transaction context, then omit `tenant_id` from normal application inserts so the database default uses `erun_current_tenant_id()`.
- Use UUIDv7 values for externally visible IDs. Keep database primary keys and API IDs aligned unless a later migration introduces private surrogate keys for a measured reason.
- Store timestamps in UTC. Database triggers own default timestamp population.
- Add foreign keys from tenant-owned tables to `tenants(tenant_id)`.

## Initial Schema Direction

- `tenants` stores tenant ID, name, type, and timestamps.
- `tenant_issuers` stores globally unique OIDC issuers mapped to tenants.
- `users` stores tenant-owned user records with a tenant-scoped `username`.
- `user_external_ids` stores one or more external identity-provider subject IDs for each user, unique per tenant, issuer, and external ID.
- `roles` stores tenant-owned role records with tenant-scoped names.
- `user_roles` stores user-to-role assignments.
- `role_permissions` stores permissions owned by roles as exact API method/path pairs or regex method/path pattern pairs.
- `reviews` stores tenant-owned review records with `name`, `target_branch`, `source_branch`, status, and last build links for failed, ready, and merged states.
- `review_merge_queue` stores waiting review queue entries. Each queued review appears at most once per tenant, and queue order is by the internal integer `review_merge_queue.review_merge_queue_id`.
- `builds` stores tenant-owned build records linked to reviews, including `successful`, `commit_id`, and `version`.
- `comments` stores tenant-owned review comments. Root comments own one review/commit/line discussion, and child comments must reference that root.
- `audit_events` stores append-only API, MCP, and CLI activity with tenant, ERun user, external identity, source-specific action fields, and event time.
- Future environment, deployment, activity, and runtime state tables should hang from `tenants(tenant_id)` and use tenant-scoped indexes.

## Audit Events

- Audit events track authenticated API, MCP, and CLI activity.
- Store audit events in ClickHouse for normal deployments.
- Required common audit fields are `tenant_id`, `erun_user_id`, `external_user_id`, `external_issuer_id`, `type`, and `created_at`.
- `type` must be one of `API`, `MCP`, or `CLI`.
- `external_issuer_id` stores the OIDC `iss` value that mapped to the tenant.
- `external_user_id` stores the external subject/user ID presented by the identity provider.
- `erun_user_id` stores the internal ERun user ID resolved from the external identity.
- API audit events must set `api_method` and `api_path`. `api_method` must be one of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, or `HEAD`.
- `api_path` must use the same canonical route template stored in `role_permissions.api_path`, such as `/v1/reviews/{review_id}` rather than a concrete request URL with IDs or query strings.
- CLI audit events must set `cli_command` and may set `cli_parameters`.
- MCP audit events must set `mcp_tool` and may set `mcp_tool_parameters`.
- Store CLI and MCP parameters as serialized text, preferably compact JSON when the caller has structured input.
- Audit events are append-only. Do not update or delete audit rows as part of normal application behavior.
- ClickHouse audit events should use a `MergeTree` table ordered for tenant/time/user/API access patterns.
- Do not put audit events in PostgreSQL unless a future requirement explicitly adds PostgreSQL audit storage.

## Review Comments

- Comments belong to reviews through `(tenant_id, review_id)`.
- Comments must include `commit_id` and positive `line`.
- `comments.status` must be one of `OPEN` or `CLOSED`.
- Root comments have `parent_comment_id IS NULL` and must have `creator_user_id`.
- Child comments have `parent_comment_id` and must not set `creator_user_id`; the root parent carries the creator for the discussion.
- Comments must not reference themselves as parents.
- Comment thread identity fields are immutable after insert: `comment_id`, `tenant_id`, `review_id`, `creator_user_id`, `parent_comment_id`, `commit_id`, and `line`.
- There can be only one root comment per tenant, review, commit, and line.
- Child comments must reference the root comment for the same tenant, review, commit, and line.
- PostgreSQL must enforce that only the root comment creator can update comment status by comparing `creator_user_id` to transaction setting `erun.user_id`.
- Do not calculate comment-thread validity in API code as the primary guard. Keep the review/commit/line and parent-child invariants in database triggers.
