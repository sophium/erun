#!/bin/sh

# End-to-end proof for the postgres-restart-destroys-database fix: a real
# postgres:18.3 container plus the real atlas migrations in this directory's
# sibling erun-backend-db module, driven the same way erun-devops/k8s renders
# it (PGDATA under the mounted volume, reset via DROP/CREATE DATABASE over
# the network). Locks three properties directly against persisted database
# state, not traces or stub calls:
#
#   1. A container restart (crash/eviction/reboot/rollout-restart stand-in)
#      never loses data that was already committed.
#   2. The reset-job.yaml SQL (DROP DATABASE ... WITH (FORCE); CREATE
#      DATABASE) genuinely wipes the database when it does run.
#   3. A schema-less database (the state defect 2 above produces, and the
#      state PGDATA-wipe used to produce) is fully repaired by re-running
#      the same atlas commands migrate.sh runs — the command the repair
#      CronJob (migrate-repair-cronjob.yaml) invokes on its own schedule.
#
# Lives beside migrate.sh rather than in erun-integration: it needs a real
# docker daemon and the real atlas CLI, neither of which the erun-devops image
# test stage carries (there is no nested docker daemon available inside a
# `docker build` RUN step), unlike the k8s/*_chart_test.sh scripts, which need
# only `helm` and do run via `make check`'s helm-chart-tests target. Run this
# one via `make test-postgres-restart` instead -- by hand, or via `erun exec
# job` in an agent env, which carries both.

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

container="erun-backend-db-migrate-test-$$"
volume="erun-backend-db-migrate-test-$$"
port=15432

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    docker volume rm "${volume}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# The official postgres image's first boot runs a temporary server to run
# init scripts, stops it, then starts the real server -- both print
# "database system is ready to accept connections", so pg_isready alone can
# catch the temporary server's brief window and report ready moments before
# it shuts down for the real restart, resetting any connection made in that
# gap. Waiting for the line to appear a given number of times in the
# container's own log (2 after first boot, one more after each restart) is
# the documented reliable signal instead.
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
    docker exec -e PGPASSWORD=testpass "${container}" psql -v ON_ERROR_STOP=1 -U erun "$@"
}

apply_migrations() {
    # 127.0.0.1, not localhost: docker's port publish binds IPv4 only, and
    # this host's resolver sometimes prefers localhost's IPv6 (::1) address,
    # which gets a connection reset rather than falling back to IPv4.
    url="postgres://erun:testpass@127.0.0.1:${port}/erun?sslmode=disable"
    (cd "${db_module}" && ERUN_DATABASE_URL="${url}" sh -c '
        set -eu
        if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-migrate-test-apply.log; then
            cat /tmp/erun-migrate-test-apply.log >&2
            if grep -q "connected database is not clean" /tmp/erun-migrate-test-apply.log; then
                atlas migrate set "${ERUN_DATABASE_BASELINE_VERSION:-20260503143000}" --env default --url "${ERUN_DATABASE_URL}"
                atlas migrate apply --env default --url "${ERUN_DATABASE_URL}"
            else
                exit 1
            fi
        fi
    ')
}

table_count() {
    psql_as -d erun -tAc "select count(*) from information_schema.tables where table_schema='public';"
}

# --- Start a real postgres:18.3, matching the chart's PGDATA/mount shape ---
docker run -d --name "${container}" \
    -e POSTGRES_DB=erun -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=testpass \
    -e PGDATA=/var/lib/postgresql/data/pgdata \
    -p "${port}:5432" \
    -v "${volume}:/var/lib/postgresql/data" \
    postgres:18.3 >/dev/null

wait_for_log_count 2 || fail "postgres did not become ready"

expected_head="$(basename -a "${db_module}"/migrations/default/*.sql | sed -n 's/^\([0-9]*\)_.*/\1/p' | sort | tail -1)"
[ -n "${expected_head}" ] || fail "could not determine the latest migration version from ${db_module}/migrations/default"

# --- 1. Fresh install applies the full migration set ---
apply_migrations
fresh_table_count="$(table_count)"
[ "${fresh_table_count}" -gt 0 ] || fail "expected at least one table after the initial migration, got ${fresh_table_count}"

# --- 2. A restart never loses committed data ---
psql_as -d erun -c "insert into tenants (name) values ('restart-survives-sentinel');" >/dev/null
docker restart "${container}" >/dev/null
wait_for_log_count 3 || fail "postgres did not come back up after restart"
survived="$(psql_as -d erun -tAc "select name from tenants where name='restart-survives-sentinel';")"
[ "${survived}" = "restart-survives-sentinel" ] || fail "a restart with no reset must never lose committed data"

# --- 3. The reset-job.yaml SQL genuinely wipes the database on purpose ---
psql_as -d postgres -c "DROP DATABASE IF EXISTS \"erun\" WITH (FORCE);" >/dev/null
psql_as -d postgres -c "CREATE DATABASE \"erun\" OWNER \"erun\";" >/dev/null
[ "$(table_count)" = "0" ] || fail "the reset must leave the database schema-less"

# --- 4. The schema-less database is fully repaired by the same command the
#         repair CronJob runs, with no Helm operation involved ---
apply_migrations
repaired_table_count="$(table_count)"
[ "${repaired_table_count}" = "${fresh_table_count}" ] ||
    fail "expected ${fresh_table_count} tables after repairing the schema-less database, got ${repaired_table_count}"
head_revision="$(psql_as -d erun -tAc "select version from atlas_schema_revisions.atlas_schema_revisions order by version desc limit 1;")"
[ "${head_revision}" = "${expected_head}" ] || fail "expected atlas head revision ${expected_head} after repair, got ${head_revision}"
tenant_count="$(psql_as -d erun -tAc "select count(*) from tenants;")"
[ "${tenant_count}" = "0" ] || fail "the repaired database must be genuinely empty, not the pre-reset data"

echo "OK: restart preserves data, reset wipes on purpose, and a schema-less database self-heals"
