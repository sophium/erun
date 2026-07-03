---
name: erun-blueprint-rls-db
description: Build a multi-tenant PostgreSQL database module following ERun's blueprint — row-level security, Atlas migrations, UUIDv7 surrogate keys, shared timestamp trigger, separate erun_tenant / erun_operations PostgreSQL roles, and the canonical tenant/issuer/user bootstrap that erun-backend-db captures — and maintain, repair, and upgrade a module it previously produced by detecting existing artifacts and entering maintenance mode instead of stopping, filling blueprint gaps without clobbering the project's own tables or committed migrations, and re-pinning the module's own version axes — the PostgreSQL major and Atlas toolchain — to their targets (it has no erun-version coupling). Use when the user says "build a multi-tenant postgres database", "create a tenant-scoped postgres schema with row-level security", "set up multi-tenant postgres migrations", "I need an erun-backend-db-shaped module", "build a multi-tenant rls db", "upgrade the multi-tenant postgres module", "repair the rls db module", "reconcile the tenant database schema to the blueprint", "bump the db module's postgres version", "maintain the erun-backend-db-shaped module", or any similar request for a new or existing tenant-scoped PostgreSQL project.
---

# Build a multi-tenant RLS database module

Produce a PostgreSQL database module following ERun's blueprint — the
same shape `erun-backend-db` captures: Atlas-managed declarative schema,
mandatory row-level security on every tenant-owned table, UUIDv7
surrogate primary keys, shared timestamp trigger, separate `erun_tenant`
/ `erun_operations` PostgreSQL roles, and the bootstrap tables
(`tenants`, `tenant_issuers`, `users`, `user_external_ids`) every
multi-tenant ERun-shaped database needs.

This skill packages ERun's accumulated best practices for multi-tenant
Postgres. Do not freelance the patterns; the conventions encoded here
are the contract.

## When to use

Trigger on user phrasings such as:

- "build a multi-tenant postgres database"
- "create a tenant-scoped postgres schema with row-level security"
- "set up multi-tenant postgres migrations"
- "I need an erun-backend-db-shaped module"
- "build a multi-tenant rls db"

## Inputs to collect

Before producing files, gather:

1. **Module name** (e.g. `acme-db`, `billing-db`). Used as the directory
   name. Default to `<tenant>-db` if a tenant identifier is in scope.
2. **Target directory** (default: current working directory).
3. **Tenant-owned tables to build** beyond the bootstrap set. For each:
   table name (plural, snake_case), columns, and natural-key uniqueness
   scope. The user can start with an empty list and add tables later.
4. **PostgreSQL major version** (default: 18). The blueprint assumes
   ≥ 18 for native `uuidv7()`. If the user is on PostgreSQL ≤ 17,
   surface that and stop — `uuidv7()` is only native in PG 18+;
   back-porting via an extension is a separate decision the user must
   make.

Do not invent these. Ask the user once, then proceed.

## What gets produced

```
<module-name>/
├── AGENTS.md
├── atlas.hcl
├── schema/
│   ├── tables/
│   │   ├── tenants.sql
│   │   ├── tenant_issuers.sql
│   │   ├── users.sql
│   │   ├── user_external_ids.sql
│   │   └── <user-supplied tables>.sql
│   ├── indexes/
│   │   ├── tenant_issuers.sql
│   │   ├── users.sql
│   │   ├── user_external_ids.sql
│   │   └── <user-supplied tables>.sql
│   ├── triggers/
│   │   ├── erun_set_timestamps.sql
│   │   ├── tenants_set_timestamps.sql
│   │   ├── users_set_timestamps.sql
│   │   └── <user-supplied tables>_set_timestamps.sql
│   ├── rls/
│   │   ├── users.sql
│   │   ├── user_external_ids.sql
│   │   └── <user-supplied tables>.sql
│   ├── fks/
│   │   └── (cross-table foreign keys when clearer outside table files)
│   └── roles.sql
└── migrations/
    └── default/
        └── (Atlas generates here on first `atlas migrate diff`)
```

Reference files for the canonical blueprint ship alongside this
`SKILL.md` under `templates/`. Use them as the source of truth; do not
freelance the boilerplate.

## Conventions (binding)

These come from `erun-backend/erun-backend-db/AGENTS.md`. Apply every
one when producing files; do not relax them for convenience.

### Identifiers

- Externally visible IDs are `UUID` columns with `DEFAULT uuidv7()`.
- Root primary keys are named `<entity>_id` (e.g. `tenants.tenant_id`),
  not generic `id`. Generic `id` is reserved for small private join
  tables.
- Foreign keys reuse the same column name where practical.

### Tenant scoping

- Every tenant-owned table has `tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id() REFERENCES tenants(tenant_id)`.
- Tenant-scoped natural-key uniqueness uses composite indexes:
  `UNIQUE (tenant_id, name)`, not `UNIQUE (name)`.
- The `tenant_id` column appears even when another FK could imply the
  tenant — tenant scoping must be directly visible in DDL, query
  predicates, and indexes.

### Timestamps

- `created_at TIMESTAMPTZ` and `updated_at TIMESTAMPTZ` on mutable
  domain tables.
- Populated by `BEFORE INSERT OR UPDATE` triggers calling the shared
  `erun_set_timestamps()` function. Trigger name:
  `<table>_set_timestamps`.
- Application inserts omit timestamp columns; trigger populates them.
  Updates preserve `created_at` and refresh `updated_at`.

### Row-level security

- Mandatory on every tenant-owned table. `ENABLE ROW LEVEL SECURITY`
  and `FORCE ROW LEVEL SECURITY` (forced, so even the table owner is
  bound).
- Policies scope by `tenant_id = erun_current_tenant_id()`.
  `erun_current_tenant_id()` returns the value of session setting
  `erun.tenant_id`, or denies access when unset.
- Two separate policies per table — one for `erun_tenant` (the normal
  tenant-scoped role) and one for `erun_operations` (cross-tenant ops
  access). Do not put an `OR` branch in the tenant policy.
- Both `USING` and `WITH CHECK` clauses, so reads, updates, deletes,
  and inserts all enforce tenant boundary.

### Roles

- `erun_tenant` — used by API/worker requests for normal tenant-scoped
  access. RLS policies named `<table>_tenant_policy`.
- `erun_operations` — used by ops tooling for cross-tenant access. RLS
  policies named `<table>_operations_policy`.
- The application login role is `GRANT`ed both, and switches via
  `SET LOCAL ROLE erun_tenant` (or `erun_operations`) before
  tenant-owned queries.
- Transaction must also `SET LOCAL erun.tenant_id = '<uuid>'` after
  `SET ROLE` so `erun_current_tenant_id()` resolves.

### Atlas layout

- `atlas.hcl` lists schema source files in dependency order:
  `roles.sql` → `tables/` → `indexes/` → `triggers/` → `rls/` → `fks/`.
- Migrations live in `migrations/default/`. Do not organize migrations
  per table — one chronological stream per dialect.
- Generate migrations via `atlas migrate diff --env default` after
  editing the declarative schema; do not hand-write SQL into
  `migrations/default/` unless fixing a generator gap.

## Step-by-step

### Step 1 — confirm inputs

Read back to the user: module name, target dir, tenant-owned table
list, PG version. If anything is unclear, ask before producing.

### Step 2 — create directory tree

```sh
mkdir -p "${target_dir}/${module}/schema"/{tables,indexes,triggers,rls,fks}
mkdir -p "${target_dir}/${module}/migrations/default"
```

### Step 3 — write the bootstrap files

Copy the reference files verbatim, substituting placeholders:

- `templates/atlas.hcl` → `${module}/atlas.hcl`
- `templates/roles.sql` → `${module}/schema/roles.sql`
- `templates/triggers/erun_set_timestamps.sql` → `${module}/schema/triggers/erun_set_timestamps.sql`
- `templates/tables/tenants.sql` → `${module}/schema/tables/tenants.sql`
- `templates/tables/tenant_issuers.sql` → `${module}/schema/tables/tenant_issuers.sql`
- `templates/tables/users.sql` → `${module}/schema/tables/users.sql`
- `templates/tables/user_external_ids.sql` → `${module}/schema/tables/user_external_ids.sql`
- `templates/indexes/users.sql` → `${module}/schema/indexes/users.sql`
- `templates/indexes/user_external_ids.sql` → `${module}/schema/indexes/user_external_ids.sql`
- `templates/triggers/tenants_set_timestamps.sql` → `${module}/schema/triggers/tenants_set_timestamps.sql`
- `templates/triggers/tenant_issuers_set_timestamps.sql` → `${module}/schema/triggers/tenant_issuers_set_timestamps.sql`
- `templates/triggers/users_set_timestamps.sql` → `${module}/schema/triggers/users_set_timestamps.sql`
- `templates/triggers/user_external_ids_set_timestamps.sql` → `${module}/schema/triggers/user_external_ids_set_timestamps.sql`
- `templates/rls/context.sql` → `${module}/schema/rls/context.sql`
- `templates/rls/users.sql` → `${module}/schema/rls/users.sql`
- `templates/rls/user_external_ids.sql` → `${module}/schema/rls/user_external_ids.sql`
- `templates/AGENTS.md` → `${module}/AGENTS.md`

### Step 4 — produce user-supplied tables

For each table name `T` the user supplied:

- `${module}/schema/tables/T.sql` — table definition. Use
  `templates/tables/_tenant_table.sql.tmpl` as the starting shape;
  replace `__TABLE__` with `T`, add the user's domain columns, set
  natural-key uniqueness as `UNIQUE (tenant_id, …)`.
- `${module}/schema/indexes/T.sql` — secondary indexes (start empty if
  the table only needs the implicit indexes from the unique
  constraint).
- `${module}/schema/triggers/T_set_timestamps.sql` — copy
  `templates/triggers/_table_set_timestamps.sql.tmpl`, replace
  `__TABLE__`.
- `${module}/schema/rls/T.sql` — copy
  `templates/rls/_tenant_table.sql.tmpl`, replace `__TABLE__`.

### Step 5 — add the user tables to `atlas.hcl`

Edit `${module}/atlas.hcl` `src = […]` list to include the new files
in the canonical order: table → indexes → triggers → rls.

### Step 6 — validate

```sh
cd "${target_dir}/${module}"
atlas schema validate --env default
```

If `atlas` is not installed, surface the install hint:
`https://release.ariga.io/atlas/atlas-linux-amd64-v1.2.0` (or use the
Homebrew tap).

To exercise the schema against a real database:

```sh
atlas migrate diff initial --env default
atlas migrate apply --env default --url "postgres://…"
```

The first command writes `migrations/default/<timestamp>_initial.sql`.
Commit it. Subsequent schema edits + `atlas migrate diff` produce
incremental migration files.

### Step 7 — bootstrap data (one-time, post-deploy)

After the first deploy, the database has no tenants. The first
authenticated identity bootstraps the system by creating an
`OPERATIONS` tenant, mapping the token issuer to that tenant, creating
the first user, and assigning `ReadAll` + `WriteAll` roles. See
`erun-backend-db/AGENTS.md` § "Multi-Tenant Database Plan" for the
rule that this only happens once — after the first tenant exists,
unknown issuers and subjects must remain unauthorized until explicitly
configured.

## Maintenance, repair & upgrade

This skill owns the module for its whole life — first scaffold **and**
ongoing upkeep of what it produced. If the target already holds a
blueprint module, do not stop: enter maintenance mode.

**Detect.** An `atlas.hcl` plus `schema/roles.sql` (or any `schema/`
tree) means the module exists. Treat the run as reconcile-in-place, not
first scaffold.

**Preview first.** Diff the on-disk module against the blueprint and the
target erun version, print the resolved plan — files to add, `atlas.hcl`
`src` entries to insert, pins to bump, `atlas migrate diff` to run — and
only write after showing it. Idempotent and in-place: safe to re-run,
touching only gaps and version pins, never genuine project content.

**Repair (fill gaps, never clobber).** Re-align the module to the
current `erun-backend-db` blueprint. Add any missing required artifact —
bootstrap tables (`tenants`, `tenant_issuers`, `users`,
`user_external_ids`), `roles.sql`, `rls/context.sql`,
`erun_set_timestamps.sql`, per-table timestamp triggers, indexes — and
fix drifted structure: absent `ENABLE`/`FORCE ROW LEVEL SECURITY`, a
missing `_tenant_policy` / `_operations_policy` pair (or a forbidden
`OR`-branch policy), missing `tenant_id` scoping or composite unique
keys, and any `atlas.hcl` `src` entry out of canonical order. Leave the
project's own tables, columns, and domain SQL untouched. **Never edit a
migration already committed under `migrations/default/`** — correct drift
with a new forward migration (`atlas migrate diff <name> --env default`),
never by rewriting an applied one.

**Upgrade (re-pin the module's own version axes).** This module has **no
erun version coupling** — it scaffolds only `atlas.hcl`, schema SQL, and
`AGENTS.md` (no chart, terraform, Dockerfile, or `VERSION`), so nothing
here pins to the env `runtimeversion` (PG 18 is not erun 1.0.x). Its
version axes are the **PostgreSQL major** (`docker://postgres/<N>/dev` in
`atlas.hcl`, and the PG version note in `AGENTS.md`) and the **Atlas
toolchain hint** — bump those to their targets, and realign to the
current `erun-backend-db` blueprint (see Repair). Then refresh derived
state: `atlas migrate diff <name> --env default` → commit the new forward
migration → `atlas migrate apply`.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| Target dir already contains an `atlas.hcl` | Not an error. Enter maintenance mode (see § "Maintenance, repair & upgrade"): reconcile gaps and re-pin in place, preview before writing, and never clobber the project's tables or committed migrations. |
| PostgreSQL < 18 detected | Stop. Explain `uuidv7()` requires PG 18+ natively; do not silently emit a custom UUIDv7 implementation. |
| `atlas` binary not on PATH at validate time | Skip validate, surface install hint, continue. The produced files are valid even without local Atlas. |
| User-supplied table name collides with a bootstrap table (`tenants`, `tenant_issuers`, `users`, `user_external_ids`) | Stop. The bootstrap names are reserved; ask the user to rename. |
| User-supplied table name is singular or PascalCase | Surface the convention (`plural snake_case`) and ask the user to confirm or rename. |

## Important

- Do not skip the role separation. Do not emit a single RLS policy
  with an `OR` branch for ops access. That pattern is explicitly
  forbidden in `erun-backend-db/AGENTS.md` § "Row-Level Security".
- Do not put `tenants` or `tenant_issuers` behind tenant-scoped RLS.
  They are the tenant resolution root and need a different access
  model — see the AGENTS.md note about security-definer functions for
  issuer lookup.
- Do not emit application-side timestamp fallback as the primary path.
  Database triggers own the default lifecycle.
- Do not use generic `id` for domain primary keys. Use explicit
  `<entity>_id` names.
- This skill encodes ERun's accumulated best practices for
  multi-tenant Postgres; do not negotiate them away to match a user's
  existing preferences. If the user's existing project conflicts with
  the blueprint, surface the conflict; do not silently relax the
  convention.
