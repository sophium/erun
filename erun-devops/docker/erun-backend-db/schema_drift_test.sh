#!/bin/sh

# Proves that the declarative schema (schema/*.sql, the source of truth
# atlas.hcl's `src` list points at) and the migration-applied state
# (migrations/default/*.sql, replayed in order) describe the exact same
# database -- against a real postgres, not a catalog-privilege guess.
#
# #2022's own root cause was a grant present in a migration but never added
# to schema/roles.sql: atlas.hcl treats schema/ as the source of truth, so
# the two silently disagreed, and the only symptom would have been a
# permission error at run time in a scheduled job. Nothing checked that they
# agreed. This is the general check: it would have caught #2022's missing
# ai_sessions DELETE grant, the reverse case (a grant declared in schema/ that
# no migration ever applies), and any other divergence -- a missing trigger,
# a differently-named constraint, a table that only exists on one side.
#
# The former Atlas binary required login to diff this function-bearing schema.
# Ptah Compat is installed as `atlas` in the images and generates these objects
# without login. Keep this independent SQL comparator: it can catch omissions
# in a migration generator as well as mistakes in the declared schema.
#
# The comparator does not ask the migration tool to model the schema. It builds
# the two states as two real postgres databases -- one via `atlas migrate apply` (pure execution, no
# parsing gate) replaying every migration, the other by concatenating
# atlas.hcl's own `src` file list (extracted from atlas.hcl itself, so the
# comparison can never hand-duplicate -- and silently drift from -- that
# ordering) and piping it through `psql` (also pure execution) -- then
# compares them with plain SQL introspection (information_schema/pg_catalog),
# ordered by name rather than physical/creation order so an incremental
# ALTER TABLE ADD COLUMN vs. a fresh CREATE TABLE's clean column order is
# never mistaken for real drift. Any genuine difference -- a missing grant,
# a missing trigger, an auto-generated vs. explicit constraint name, a
# missing table -- survives that normalization and fails the diff.
#
# Lives beside migrate_test.sh/retention_test.sh/retention_grants_test.sh,
# same shape and same reason for staying out of `make check`: a real docker
# daemon and the atlas CLI, neither available in the bare test-stage image.
# Run via `make test-schema-drift` -- by hand, or via `erun exec job` in an
# agent env -- before merging a change to atlas.hcl, schema/, or
# migrations/default/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
db_module="$(cd "${script_dir}/../../../erun-backend/erun-backend-db" && pwd)"

command -v docker >/dev/null 2>&1 || {
    echo "FAIL: docker is required" >&2
    exit 1
}
command -v atlas >/dev/null 2>&1 || {
    echo "FAIL: atlas is required" >&2
    exit 1
}

container="erun-backend-db-drift-test-$$"
port=15435
tmpdir="$(mktemp -d)"

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    rm -rf "${tmpdir}"
}
trap cleanup EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# See migrate_test.sh's own comment: the official postgres image's first boot
# runs a temporary server for init scripts, then restarts into the real one,
# both logging "database system is ready to accept connections".
wait_for_log_count() {
    target="$1"
    i=0
    while [ "$i" -lt 60 ]; do
        count="$(docker logs "${container}" 2>&1 | grep -c 'database system is ready to accept connections')"
        [ "${count}" -ge "${target}" ] && return 0
        i=$((i + 1))
        sleep 1
    done
    return 1
}

psql_as() {
    docker exec -i -e PGPASSWORD=testpass "${container}" psql -v ON_ERROR_STOP=1 -U erun "$@"
}

docker run -d --name "${container}" \
    -e POSTGRES_DB=erun -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=testpass \
    -p "${port}:5432" \
    postgres:18.3 >/dev/null

wait_for_log_count 2 || fail "postgres did not become ready"

psql_as -d erun -c "CREATE DATABASE erun_migrated;" -c "CREATE DATABASE erun_declared;" >/dev/null

# --- Side 1: the migration-applied state ---
url="postgres://erun:testpass@127.0.0.1:${port}/erun_migrated?sslmode=disable"
(cd "${db_module}" && ERUN_DATABASE_URL="${url}" sh -c '
    set -eu
    if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-drift-test-apply.log; then
        cat /tmp/erun-drift-test-apply.log >&2
        exit 1
    fi
')

# --- Side 2: the declared schema state, applied in atlas.hcl's own `src`
#     order so this test can never drift from the ordering atlas itself uses ---
declared_schema="${tmpdir}/declared_schema.sql"
: >"${declared_schema}"
for f in $(grep -oE 'file://schema/[^"]+\.sql' "${db_module}/atlas.hcl" | sed 's#file://##'); do
    cat "${db_module}/${f}" >>"${declared_schema}"
    echo >>"${declared_schema}"
done
[ -s "${declared_schema}" ] || fail "no schema/*.sql entries found in atlas.hcl -- src list parsing is broken"

if ! psql_as -d erun_declared -v ON_ERROR_STOP=1 <"${declared_schema}" >"${tmpdir}/declared_apply.log" 2>&1; then
    cat "${tmpdir}/declared_apply.log" >&2
    fail "applying schema/*.sql in atlas.hcl's declared order failed -- likely a src ordering bug (a file referencing an object a later file creates)"
fi

# --- Compare the two states via plain SQL introspection, ordered by name
#     (never by ordinal/creation position) so incremental-migration column
#     order is never mistaken for drift. Scoped to the `public` schema only,
#     which excludes atlas's own `atlas_schema_revisions` bookkeeping. ---
introspect_sql="${tmpdir}/introspect.sql"
cat >"${introspect_sql}" <<'SQL'
\pset format unaligned
\pset tuples_only on
\pset fieldsep '|'

SELECT '### ROLES' AS marker;
SELECT rolname, rolcanlogin FROM pg_roles WHERE rolname IN ('erun_tenant','erun_operations') ORDER BY 1;

SELECT '### SCHEMA USAGE' AS marker;
SELECT rolname, has_schema_privilege(rolname, 'public', 'USAGE')
FROM pg_roles WHERE rolname IN ('erun_tenant','erun_operations') ORDER BY 1;

SELECT '### COLUMNS' AS marker;
SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default,''), is_identity, coalesce(identity_generation,'')
FROM information_schema.columns
WHERE table_schema='public'
ORDER BY table_name, column_name;

SELECT '### CONSTRAINTS' AS marker;
SELECT c.conrelid::regclass::text, c.conname, pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_namespace n ON n.oid = c.connamespace
WHERE n.nspname = 'public'
ORDER BY 1, 2;

SELECT '### INDEXES' AS marker;
SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname='public' ORDER BY tablename, indexname;

SELECT '### TRIGGERS' AS marker;
SELECT event_object_table, trigger_name, action_timing, event_manipulation, action_statement
FROM information_schema.triggers
WHERE trigger_schema='public'
ORDER BY event_object_table, trigger_name, event_manipulation;

SELECT '### RLS FLAGS' AS marker;
SELECT relname, relrowsecurity, relforcerowsecurity
FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND relkind='r'
ORDER BY relname;

SELECT '### RLS POLICIES' AS marker;
SELECT tablename, policyname, permissive, roles::text, cmd, coalesce(qual,''), coalesce(with_check,'')
FROM pg_policies WHERE schemaname='public' ORDER BY tablename, policyname;

SELECT '### TABLE GRANTS' AS marker;
SELECT table_name, grantee, privilege_type FROM information_schema.role_table_grants
WHERE table_schema='public' AND grantee IN ('erun_tenant','erun_operations')
ORDER BY table_name, grantee, privilege_type;

SELECT '### COLUMN GRANTS' AS marker;
SELECT table_name, column_name, grantee, privilege_type FROM information_schema.column_privileges
WHERE table_schema='public' AND grantee IN ('erun_tenant','erun_operations')
ORDER BY table_name, column_name, grantee, privilege_type;

SELECT '### FUNCTIONS' AS marker;
SELECT p.proname, pg_get_functiondef(p.oid)
FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='public'
ORDER BY p.proname;
SQL

psql_as -d erun_migrated <"${introspect_sql}" >"${tmpdir}/migrated.txt" 2>&1
psql_as -d erun_declared <"${introspect_sql}" >"${tmpdir}/declared.txt" 2>&1

if ! diff -u "${tmpdir}/declared.txt" "${tmpdir}/migrated.txt" >"${tmpdir}/drift.diff"; then
    echo "FAIL: schema/*.sql (declared, atlas.hcl's source of truth) and migrations/default/*.sql (applied) describe different databases:" >&2
    echo >&2
    cat "${tmpdir}/drift.diff" >&2
    echo >&2
    echo "A '+' line exists only in the migrations -- add it to the matching schema/*.sql file (this was #2022's exact bug, a grant)." >&2
    echo "A '-' line exists only in the declared schema -- add a migration that applies it, or the declared object was never really shipped." >&2
    exit 1
fi

echo "OK: the declared schema (schema/*.sql via atlas.hcl's src order) and the migration-applied state describe the same database"

# Exercise generation in a copy: the committed schema and migration history
# stay untouched. An unchanged desired schema must not produce a migration.
work_module="${tmpdir}/db"
cp -R "${db_module}" "${work_module}"
cp "${work_module}/migrations/default/atlas.sum" "${tmpdir}/baseline.sum"
(cd "${work_module}" && atlas migrate diff unchanged --env default)
cmp "${tmpdir}/baseline.sum" "${work_module}/migrations/default/atlas.sum" \
    || fail "unchanged desired schema generated a migration"

cat >"${work_module}/schema/ptah_probe.sql" <<'SQL'
CREATE TABLE ptah_probe (
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 tenant_id uuid NOT NULL DEFAULT erun_current_tenant_id(),
 name text NOT NULL,
 created_at timestamptz,
 updated_at timestamptz
);
CREATE TRIGGER ptah_probe_set_timestamps BEFORE INSERT OR UPDATE ON ptah_probe
 FOR EACH ROW EXECUTE FUNCTION erun_set_timestamps();
ALTER TABLE ptah_probe ENABLE ROW LEVEL SECURITY;
ALTER TABLE ptah_probe FORCE ROW LEVEL SECURITY;
CREATE POLICY ptah_probe_tenant ON ptah_probe TO erun_tenant
 USING (tenant_id = erun_current_tenant_id()) WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY ptah_probe_operations ON ptah_probe TO erun_operations USING (true) WITH CHECK (true);
GRANT SELECT, INSERT ON ptah_probe TO erun_tenant, erun_operations;
GRANT UPDATE (name) ON ptah_probe TO erun_tenant;
SQL
awk '/^  ]/ { print "    \"file://schema/ptah_probe.sql\"," } { print }' \
    "${work_module}/atlas.hcl" >"${tmpdir}/atlas.hcl"
mv "${tmpdir}/atlas.hcl" "${work_module}/atlas.hcl"
(cd "${work_module}" && atlas migrate diff generated_objects --env default)
if cmp -s "${tmpdir}/baseline.sum" "${work_module}/migrations/default/atlas.sum"; then
    fail "new trigger, policies, and grants produced no migration"
fi
(cd "${work_module}" && atlas migrate apply --env default --url "${url}")
psql_as -d erun_declared <"${work_module}/schema/ptah_probe.sql" >/dev/null
psql_as -d erun_migrated <"${introspect_sql}" >"${tmpdir}/generated.txt" 2>&1
psql_as -d erun_declared <"${introspect_sql}" >"${tmpdir}/expected.txt" 2>&1
diff -u "${tmpdir}/expected.txt" "${tmpdir}/generated.txt" \
    || fail "generated migration differs from the desired PostgreSQL objects"

# Effective behavior matters as well as catalog definitions.
psql_as -d erun_migrated <<'SQL'
\set ON_ERROR_STOP on
BEGIN;
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_class WHERE oid = 'ptah_probe'::regclass AND relrowsecurity AND relforcerowsecurity) THEN
  RAISE EXCEPTION 'RLS is not enabled and forced';
 END IF;
 IF (SELECT count(*) FROM pg_policy WHERE polrelid = 'ptah_probe'::regclass) <> 2 THEN
  RAISE EXCEPTION 'policies missing';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'ptah_probe'::regclass AND tgname = 'ptah_probe_set_timestamps' AND NOT tgisinternal) THEN
  RAISE EXCEPTION 'timestamp trigger missing';
 END IF;
END $$;
INSERT INTO ptah_probe (tenant_id, name) VALUES
 ('11111111-1111-1111-1111-111111111111', 'tenant-one'),
 ('22222222-2222-2222-2222-222222222222', 'tenant-two');
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM ptah_probe WHERE created_at IS NULL OR updated_at IS NULL) THEN
  RAISE EXCEPTION 'timestamp trigger did not execute';
 END IF;
END $$;
SET LOCAL ROLE erun_tenant;
SET LOCAL erun.tenant_id = '11111111-1111-1111-1111-111111111111';
DO $$
BEGIN
 IF (SELECT count(*) FROM ptah_probe) <> 1 THEN
  RAISE EXCEPTION 'tenant isolation failed';
 END IF;
END $$;
UPDATE ptah_probe SET name = 'tenant-one-updated';
DO $$
BEGIN
 BEGIN
  INSERT INTO ptah_probe (tenant_id, name) VALUES ('22222222-2222-2222-2222-222222222222', 'forbidden');
  RAISE EXCEPTION 'cross-tenant insert unexpectedly succeeded';
 EXCEPTION WHEN insufficient_privilege THEN NULL;
 END;
 BEGIN
  UPDATE ptah_probe SET tenant_id = '22222222-2222-2222-2222-222222222222';
  RAISE EXCEPTION 'column privilege unexpectedly widened';
 EXCEPTION WHEN insufficient_privilege THEN NULL;
 END;
END $$;
RESET ROLE;
SET LOCAL ROLE erun_operations;
DO $$
BEGIN
 IF (SELECT count(*) FROM ptah_probe) <> 2 THEN
  RAISE EXCEPTION 'operations access failed';
 END IF;
END $$;
ROLLBACK;
\echo trigger, tenant isolation, operations access, and column grant: PASS
SQL
cp "${work_module}/migrations/default/atlas.sum" "${tmpdir}/generated.sum"
(cd "${work_module}" && atlas migrate diff settled --env default)
cmp "${tmpdir}/generated.sum" "${work_module}/migrations/default/atlas.sum" \
    || fail "generated migration did not converge"
echo "OK: generated triggers, RLS policies, and grants apply and converge without manual SQL"
