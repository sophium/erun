#!/bin/sh

# End-to-end proof that the ai_sessions retention sweep
# (erun-backend-db/retention/ai_sessions.sql) deletes exactly the rows the
# design in erun-backend-db/AGENTS.md ("AI Sessions") says it should, and
# nothing else -- against a real postgres:18.3, not a mock. Locks four
# properties directly against persisted database state:
#
#   1. a session whose last event is 'exit' past the 14-day age bound is
#      deleted.
#   2. a session whose last event is 'exit' within the age bound survives.
#   3. a session whose last event is anything other than 'exit' (still
#      busy/idle/awaiting-input) is exempt regardless of age.
#   4. exited sessions beyond the 500-most-recent-per-(tenant, environment)
#      count cap are deleted even when every row is well within the age
#      bound, and the cap is per-environment, not per-tenant: a second,
#      quiet environment in the same tenant is untouched by a noisy one's
#      count.
#
# It also proves dry_run=true reports the same counts but deletes nothing.
#
# Lives beside retention_test.sh and follows the same shape: a real docker
# daemon runs postgres, the real migrations are applied via the atlas CLI,
# and the retention SQL is run directly via psql against the exposed port.
# Run via `make test-retention` -- by hand, or via `erun exec job` in an
# agent env -- rather than `make check`, which has neither a docker daemon
# nor the atlas CLI available inside its bare test image.

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

container="erun-backend-db-ai-sessions-retention-test-$$"
volume="erun-backend-db-ai-sessions-retention-test-$$"
port=15435

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    docker volume rm "${volume}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

psql_as() {
    docker exec -i -e PGPASSWORD=testpass "${container}" psql -v ON_ERROR_STOP=1 -U erun "$@"
}

count_query() {
    psql_as -d erun -tAc "$1" | tr -d '[:space:]'
}

docker run -d --name "${container}" \
    -e POSTGRES_DB=erun -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=testpass \
    -e PGDATA=/var/lib/postgresql/data/pgdata \
    -p "${port}:5432" \
    -v "${volume}:/var/lib/postgresql/data" \
    postgres:18.3 >/dev/null

# postgres restarts once during initdb (a transient instance runs the init
# scripts, then a real restart serves traffic) -- pg_isready succeeds against
# that transient instance too, so polling it alone races the restart and
# intermittently connects just as it tears down. The ready message appears
# twice in the container log, once per instance; waiting for the second
# occurrence is what actually observes the final instance.
i=0
ready_count=0
while [ "$i" -lt 60 ]; do
    ready_count=$(docker logs "${container}" 2>&1 | grep -c "database system is ready to accept connections" || true)
    [ "${ready_count}" -ge 2 ] && break
    i=$((i + 1))
    sleep 1
done
[ "${ready_count}" -ge 2 ] || fail "postgres did not become ready"

url="postgres://erun:testpass@localhost:${port}/erun?sslmode=disable"
(cd "${db_module}" && ERUN_DATABASE_URL="${url}" sh -c '
    set -eu
    if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-ai-sessions-retention-test-apply.log; then
        cat /tmp/erun-ai-sessions-retention-test-apply.log >&2
        exit 1
    fi
')

# --- Fixtures ---
# age-tenant/env: one exited session 30 days old (past the 14-day bound,
# eligible), one exited session 1 day old (within it, survives), one
# turn-start session 30 days old (exempt regardless of age).
# count-tenant, with two environments env-noisy and env-quiet: env-noisy
# has 505 exited sessions seconds apart (5 beyond the 500-row cap, eligible);
# env-quiet has 10 exited sessions seconds apart, all well within both
# bounds -- proving the cap is per-(tenant, environment), not per-tenant.
psql_as -d erun <<'SQL'
INSERT INTO tenants (tenant_id, name) VALUES
  ('00000000-0000-0000-0000-000000000001', 'age-tenant'),
  ('00000000-0000-0000-0000-000000000002', 'count-tenant');

INSERT INTO environments (environment_id, tenant_id, name, type) VALUES
  ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-000000000001', 'env1', 'local-agent'),
  ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000002', 'env-noisy', 'local-agent'),
  ('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000002', 'env-quiet', 'local-agent');

INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at) VALUES
  ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'exit-old', 'exit', now() - interval '30 days'),
  ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'exit-young', 'exit', now() - interval '1 day'),
  ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'busy-old', 'turn-start', now() - interval '30 days');

INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at)
SELECT '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000201', 'noisy-' || i, 'exit',
       now() - (i * interval '1 second')
FROM generate_series(1, 505) AS i;

INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at)
SELECT '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000202', 'quiet-' || i, 'exit',
       now() - (i * interval '1 second')
FROM generate_series(1, 10) AS i;
SQL

initial_sessions="$(count_query "select count(*) from ai_sessions;")"
[ "${initial_sessions}" = "518" ] || fail "expected 518 fixture ai_sessions (3 + 505 + 10), got ${initial_sessions}"

# --- Dry run reports the exact eligible count and deletes nothing ---
report="$(docker cp "${db_module}/retention/ai_sessions.sql" "${container}:/tmp/ai_sessions.sql" &&
    psql_as -d erun -v "dry_run=true" -f /tmp/ai_sessions.sql)"
printf '%s\n' "${report}" | grep -E '^\s*ai_sessions\s*\|\s*6\s*$' >/dev/null ||
    fail "dry run must report 6 ai_sessions eligible (1 age-bound + 5 count-cap on env-noisy), got:\n${report}"

[ "$(count_query "select count(*) from ai_sessions;")" = "${initial_sessions}" ] || fail "dry run must not delete any ai_sessions"

# --- Real run deletes exactly the eligible rows ---
psql_as -d erun -v "dry_run=false" -f /tmp/ai_sessions.sql >/dev/null

# 1. age-tenant: the 30-day-old exit is gone; the 1-day-old exit and the
#    30-day-old non-exit both survive.
[ "$(count_query "select count(*) from ai_sessions where session_id = 'exit-old';")" = "0" ] ||
    fail "an exited session past the age bound must be deleted"
[ "$(count_query "select count(*) from ai_sessions where session_id = 'exit-young';")" = "1" ] ||
    fail "an exited session within the age bound must survive"
[ "$(count_query "select count(*) from ai_sessions where session_id = 'busy-old';")" = "1" ] ||
    fail "a non-exit session must survive no matter how old it is"

# 2. env-noisy keeps exactly 500 -- the 5 oldest are gone.
[ "$(count_query "select count(*) from ai_sessions where environment_id = '00000000-0000-0000-0000-000000000201';")" = "500" ] ||
    fail "the noisy environment must keep exactly 500 sessions"
[ "$(count_query "select count(*) from ai_sessions where environment_id = '00000000-0000-0000-0000-000000000201' and session_id in ('noisy-501','noisy-502','noisy-503','noisy-504','noisy-505');")" = "0" ] ||
    fail "the noisy environment's 5 oldest sessions must be deleted"
[ "$(count_query "select count(*) from ai_sessions where environment_id = '00000000-0000-0000-0000-000000000201' and session_id = 'noisy-1';")" = "1" ] ||
    fail "the noisy environment's newest session must survive"

# 3. env-quiet, same tenant, is untouched -- the cap is per-environment.
[ "$(count_query "select count(*) from ai_sessions where environment_id = '00000000-0000-0000-0000-000000000202';")" = "10" ] ||
    fail "a quiet environment in the same tenant as a noisy one must be untouched"

echo "OK: ai_sessions retention deletes exactly the age- and per-environment-count-bound exited sessions the design specifies, and nothing else"
