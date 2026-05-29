# AGENTS.md

Module-specific guidance for this multi-tenant database. Follow your
repository root `AGENTS.md` first, then apply this file.

## Module role

- This is an Atlas-managed PostgreSQL database module built from the
  ERun `erun-backend-db` blueprint via the `erun-blueprint-rls-db`
  skill.
- The default OLTP database is PostgreSQL 18 or newer (native `uuidv7()`).
- Schema changes flow: edit declarative files under `schema/` →
  `atlas migrate diff --env default` → commit the generated SQL in
  `migrations/default/` → `atlas migrate apply --env default --url …`.

## Conventions

- Externally visible IDs are `UUID` with `DEFAULT uuidv7()`.
- Tenant-owned tables have `tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id() REFERENCES tenants(tenant_id)`.
- Tenant-scoped natural-key uniqueness uses composite indexes:
  `UNIQUE (tenant_id, …)`.
- `created_at` / `updated_at` are `TIMESTAMPTZ` populated by triggers
  calling `erun_set_timestamps()`. Application inserts omit them.
- RLS is mandatory on every tenant-owned table with both `tenant_isolation`
  (for `erun_tenant`) and `operations_access` (for `erun_operations`)
  policies. Do not collapse them.

## Adding a tenant-owned table

1. Copy the reference files from the blueprint skill if you still have
   them, otherwise follow the shape of `schema/tables/users.sql` and its sibling
   RLS / trigger / index files.
2. Create:
   - `schema/tables/<table>.sql` — definition with `tenant_id`, FK,
     uniqueness scoped by `tenant_id`.
   - `schema/indexes/<table>.sql` — at minimum a `(tenant_id)` index.
   - `schema/triggers/<table>_set_timestamps.sql` — trigger calling
     `erun_set_timestamps()`.
   - `schema/rls/<table>.sql` — two policies, one per role.
3. Add the four files to `atlas.hcl` `src = […]` in canonical order:
   table → indexes → triggers → rls.
4. Grant CRUD on the new table to both roles in `schema/roles.sql`.
5. `atlas migrate diff <descriptor> --env default` to generate the
   migration.
6. `atlas migrate apply --env default --url "postgres://…"` against a
   dev database to confirm the migration applies cleanly.

## Application-side contract

Before any tenant-owned query, the application transaction must:

```sql
SET LOCAL ROLE erun_tenant;
SET LOCAL erun.tenant_id = '<resolved-tenant-uuid>';
```

For ops tooling that needs cross-tenant access:

```sql
SET LOCAL ROLE erun_operations;
-- erun.tenant_id is not needed; operations_access policy is unconditional.
```

For user-owned mutations (e.g. closing a comment by its creator):

```sql
SET LOCAL erun.user_id = '<resolved-user-uuid>';
```

The application login role must have been `GRANT`ed both `erun_tenant`
and `erun_operations` during deployment.

## Bootstrap

After the first deploy, the database has no tenants. The first
authenticated identity may bootstrap by creating an `OPERATIONS` tenant,
mapping the token issuer to that tenant, creating the first user, and
assigning the equivalent of `ReadAll` + `WriteAll`. Once any tenant
exists, do not bootstrap another operations tenant — unknown issuers
must remain unauthorized until explicitly configured.
