#!/bin/sh

# End-to-end proof that the invites/invite_requests retention sweep
# (erun-backend-db/retention/invites_invite_requests.sql) deletes exactly
# the rows the design in erun-backend-db/AGENTS.md ("Invites And Invite
# Requests") says it should, and nothing else -- against a real
# postgres:18.3, not a mock. Locks these properties directly against
# persisted database state:
#
#   1. invite_requests: a PENDING request is exempt regardless of age; an
#      APPROVED/DECLINED request past the 180-day age bound (on updated_at)
#      is deleted; one within it survives; requests beyond the
#      10,000-row platform-wide count cap are deleted even within the age
#      bound.
#   2. invites: a live (unconsumed, unexpired) invite is exempt; a consumed
#      invite past the 365-day age bound (on consumed_at) is deleted; one
#      within it survives; an unconsumed expired invite past the 30-day age
#      bound (on expires_at) is deleted; one within it survives; either
#      population beyond the 10,000-per-tenant count cap is deleted even
#      within its age bound.
#   3. a consumed invite still referenced by invite_requests.minted_invite_id
#      survives even past its own age bound -- the RESTRICT-FK guard -- and
#      is deleted once that reference is gone (invite_requests is pruned
#      first, in the same sweep).
#
# It also proves dry_run=true reports the same counts but deletes nothing,
# and that both runs record their outcome in retention_runs (eligible_count
# and deleted_count, tagged with dry_run) -- invites_invite_requests.sql
# previously computed these counts for its own report but never persisted
# them, unlike comments_releases.sql; this test locks the fix.
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

container="erun-backend-db-invites-retention-test-$$"
volume="erun-backend-db-invites-retention-test-$$"
port=15436

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
    if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-invites-retention-test-apply.log; then
        cat /tmp/erun-invites-retention-test-apply.log >&2
        exit 1
    fi
')

# --- Fixtures ---
# tenant t1: one consumed invite 400 days old (past the 365-day bound,
# eligible), one consumed invite 10 days old (within it, survives), one
# unconsumed-expired invite 40 days past expiry (past the 30-day bound,
# eligible), one unconsumed-expired invite 5 days past expiry (within it,
# survives), one live (unconsumed, unexpired) invite (exempt regardless).
# tenant t1 also gets a consumed invite still referenced by a PENDING
# invite_request's minted_invite_id, 400 days old -- guarded, must survive.
# invite_requests (global, no tenant): one PENDING 400 days old (exempt),
# one APPROVED 400 days old (past the 180-day bound, eligible), one
# DECLINED 10 days old (within it, survives).
# tenant t2 (invites-count-tenant): 10,005 consumed invites seconds apart --
# only the 5 oldest (beyond the 10,000-row cap) are eligible.
psql_as -d erun <<'SQL'
INSERT INTO tenants (tenant_id, name) VALUES
  ('00000000-0000-0000-0000-000000000001', 't1'),
  ('00000000-0000-0000-0000-000000000002', 'invites-count-tenant');

INSERT INTO users (user_id, tenant_id, username) VALUES
  ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-000000000001', 'u1'),
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-000000000002', 'u2');

INSERT INTO invites (invite_id, tenant_id, created_by_user_id, issuer, token, email, expires_at, consumed_at, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'iss', 'tok-consumed-old', 'a@x.com', now() - interval '399 days', now() - interval '400 days', now() - interval '401 days', now() - interval '400 days'),
  ('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'iss', 'tok-consumed-young', 'b@x.com', now() - interval '9 days', now() - interval '10 days', now() - interval '11 days', now() - interval '10 days'),
  ('00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'iss', 'tok-expired-old', 'c@x.com', now() - interval '40 days', NULL, now() - interval '50 days', now() - interval '50 days'),
  ('00000000-0000-0000-0000-000000000204', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'iss', 'tok-expired-young', 'd@x.com', now() - interval '5 days', NULL, now() - interval '10 days', now() - interval '10 days'),
  ('00000000-0000-0000-0000-000000000205', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'iss', 'tok-live', 'e@x.com', now() + interval '10 days', NULL, now(), now()),
  ('00000000-0000-0000-0000-000000000206', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 'iss', 'tok-consumed-guarded', 'f@x.com', now() - interval '399 days', now() - interval '400 days', now() - interval '401 days', now() - interval '400 days');

INSERT INTO invite_requests (issuer, subject, kind, tenant_name, status, minted_invite_id, decided_by_user_id, decline_reason, updated_at) VALUES
  ('iss', 'sub-pending', 'JOIN_TENANT', 't1', 'PENDING', '00000000-0000-0000-0000-000000000206', NULL, NULL, now() - interval '400 days'),
  ('iss', 'sub-approved-old', 'JOIN_TENANT', 't1', 'APPROVED', NULL, '00000000-0000-0000-0000-000000000101', NULL, now() - interval '400 days'),
  ('iss', 'sub-declined-young', 'JOIN_TENANT', 't1', 'DECLINED', NULL, '00000000-0000-0000-0000-000000000101', 'no capacity', now() - interval '10 days');

INSERT INTO invites (tenant_id, created_by_user_id, issuer, token, expires_at, consumed_at, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000102', 'iss', 'count-' || i,
       now() + interval '1 day', now() - (i * interval '1 second'), now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 10005) AS i;
SQL

initial_invites="$(count_query "select count(*) from invites;")"
initial_requests="$(count_query "select count(*) from invite_requests;")"
[ "${initial_invites}" = "10011" ] || fail "expected 10011 fixture invites (6 + 10005 count-cap), got ${initial_invites}"
[ "${initial_requests}" = "3" ] || fail "expected 3 fixture invite_requests, got ${initial_requests}"

# --- Dry run reports the exact eligible counts and deletes nothing ---
report="$(docker cp "${db_module}/retention/invites_invite_requests.sql" "${container}:/tmp/invites_invite_requests.sql" &&
    psql_as -d erun -v "dry_run=true" -f /tmp/invites_invite_requests.sql)"
printf '%s\n' "${report}" | grep -E '^\s*invite_requests\s*\|\s*1\s*$' >/dev/null ||
    fail "dry run must report 1 invite_requests eligible (sub-approved-old), got:\n${report}"
printf '%s\n' "${report}" | grep -E '^\s*invites\s*\|\s*7\s*$' >/dev/null ||
    fail "dry run must report 7 invites eligible (consumed-old + expired-old + 5 count-cap; the guarded consumed invite must NOT be counted), got:\n${report}"

[ "$(count_query "select count(*) from invites;")" = "${initial_invites}" ] || fail "dry run must not delete any invites"
[ "$(count_query "select count(*) from invite_requests;")" = "${initial_requests}" ] || fail "dry run must not delete any invite_requests"

# --- The dry run recorded its outcome: eligible counts, zero deleted ---
[ "$(count_query "select eligible_count from retention_runs where policy_name='invites_invite_requests' and table_name='invite_requests' and dry_run=true order by created_at desc limit 1;")" = "1" ] ||
    fail "the dry run must record 1 invite_requests eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='invites_invite_requests' and table_name='invite_requests' and dry_run=true order by created_at desc limit 1;")" = "0" ] ||
    fail "the dry run must record 0 invite_requests deleted in retention_runs"
[ "$(count_query "select eligible_count from retention_runs where policy_name='invites_invite_requests' and table_name='invites' and dry_run=true order by created_at desc limit 1;")" = "7" ] ||
    fail "the dry run must record 7 invites eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='invites_invite_requests' and table_name='invites' and dry_run=true order by created_at desc limit 1;")" = "0" ] ||
    fail "the dry run must record 0 invites deleted in retention_runs"

# --- Real run deletes exactly the eligible rows ---
psql_as -d erun -v "dry_run=false" -f /tmp/invites_invite_requests.sql >/dev/null

# --- The real run recorded its outcome: eligible counts, matching deleted counts ---
[ "$(count_query "select eligible_count from retention_runs where policy_name='invites_invite_requests' and table_name='invite_requests' and dry_run=false order by created_at desc limit 1;")" = "1" ] ||
    fail "the real run must record 1 invite_requests eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='invites_invite_requests' and table_name='invite_requests' and dry_run=false order by created_at desc limit 1;")" = "1" ] ||
    fail "the real run must record 1 invite_requests deleted in retention_runs"
[ "$(count_query "select eligible_count from retention_runs where policy_name='invites_invite_requests' and table_name='invites' and dry_run=false order by created_at desc limit 1;")" = "7" ] ||
    fail "the real run must record 7 invites eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='invites_invite_requests' and table_name='invites' and dry_run=false order by created_at desc limit 1;")" = "7" ] ||
    fail "the real run must record 7 invites deleted in retention_runs"

# 1. invite_requests: PENDING survives regardless of age, APPROVED-old is
#    gone, DECLINED-young survives.
[ "$(count_query "select count(*) from invite_requests where subject = 'sub-pending';")" = "1" ] ||
    fail "a PENDING request must survive no matter how old it is"
[ "$(count_query "select count(*) from invite_requests where subject = 'sub-approved-old';")" = "0" ] ||
    fail "an APPROVED request past the age bound must be deleted"
[ "$(count_query "select count(*) from invite_requests where subject = 'sub-declined-young';")" = "1" ] ||
    fail "a DECLINED request within the age bound must survive"

# 2. invites: consumed-old is gone, consumed-young survives, expired-old is
#    gone, expired-young survives, the live invite survives.
[ "$(count_query "select count(*) from invites where token = 'tok-consumed-old';")" = "0" ] ||
    fail "a consumed invite past the 365-day bound must be deleted"
[ "$(count_query "select count(*) from invites where token = 'tok-consumed-young';")" = "1" ] ||
    fail "a consumed invite within the 365-day bound must survive"
[ "$(count_query "select count(*) from invites where token = 'tok-expired-old';")" = "0" ] ||
    fail "an unconsumed expired invite past the 30-day bound must be deleted"
[ "$(count_query "select count(*) from invites where token = 'tok-expired-young';")" = "1" ] ||
    fail "an unconsumed expired invite within the 30-day bound must survive"
[ "$(count_query "select count(*) from invites where token = 'tok-live';")" = "1" ] ||
    fail "a live invite must survive regardless of the sweep"

# 3. tok-consumed-guarded: once the PENDING request referencing it was
#    itself exempt (never pruned this round), the guarded invite must
#    still survive despite being past its own 365-day bound.
[ "$(count_query "select count(*) from invites where token = 'tok-consumed-guarded';")" = "1" ] ||
    fail "a consumed invite still referenced by invite_requests.minted_invite_id must survive"

# 4. invites-count-tenant keeps exactly 10,000 -- the 5 oldest are gone.
[ "$(count_query "select count(*) from invites where tenant_id = '00000000-0000-0000-0000-000000000002';")" = "10000" ] ||
    fail "the count-cap tenant must keep exactly 10000 invites"
[ "$(count_query "select count(*) from invites where tenant_id = '00000000-0000-0000-0000-000000000002' and token in ('count-10001','count-10002','count-10003','count-10004','count-10005');")" = "0" ] ||
    fail "the count-cap tenant's 5 oldest invites must be deleted"
[ "$(count_query "select count(*) from invites where tenant_id = '00000000-0000-0000-0000-000000000002' and token = 'count-1';")" = "1" ] ||
    fail "the count-cap tenant's newest invite must survive"

echo "OK: invites/invite_requests retention deletes exactly the age- and count-bound rows the design specifies, respects the minted_invite_id guard, and touches nothing else"
