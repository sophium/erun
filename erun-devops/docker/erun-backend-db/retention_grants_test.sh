#!/bin/sh

# End-to-end proof that erun_operations (the role every retention policy in
# erun-backend-db/retention/ runs as, via `SET ROLE erun_operations;`) can
# actually DELETE from every table a retention policy sweeps -- against a
# real postgres:18.3, not a catalog-privilege query. #1972's own ai_sessions
# retention design shipped with a gap it named but did not close: the role
# had SELECT/INSERT/UPDATE/REFERENCES on ai_sessions but no DELETE, so that
# policy could never delete a row even once implemented. This locks that the
# gap is closed, and that it was the *only* gap: every other table under an
# implemented or designed retention policy (the #1968 six-table sweep --
# comments, releases, reviews, ai_sessions, invites, invite_requests -- plus
# builds/gate_runs, which predate the sweep) grants DELETE too.
#
# A has_table_privilege() catalog check can be satisfied by a GRANT that
# looks right on paper while row-level security still blocks the actual
# statement (a missing USING/WITH CHECK clause, a policy scoped to the wrong
# role) -- so every assertion here runs a real INSERT then DELETE inside a
# transaction that rolls back, never a privilege lookup. audit_events and
# usage_events are asserted the other way: they must NOT be deletable by
# erun_operations, since #1959's design deliberately withholds DELETE from
# both pending a compliance decision -- a regression there would be a
# silent privilege escalation, not a bug fix.
#
# Lives beside retention_test.sh and migrate_test.sh, same shape: a real
# docker daemon runs postgres, the real migrations from the sibling
# erun-backend-db module are applied via the atlas CLI, and every statement
# runs directly via psql against the exposed port. Run via
# `make test-retention-grants` -- by hand, or via `erun exec job` in an
# agent env -- rather than `make check`, which has neither a docker daemon
# nor the atlas CLI available inside its bare test image (same constraint
# as test-postgres-restart and test-retention).

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

container="erun-backend-db-grants-test-$$"
volume="erun-backend-db-grants-test-$$"
port=15434

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    docker volume rm "${volume}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# See retention_test.sh's own wait_for_log_count for why one appearance of
# the "ready" line is not enough: the official postgres image restarts once
# after running init scripts, and a single-match wait can catch the
# temporary server's brief window.
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
    -e PGDATA=/var/lib/postgresql/data/pgdata \
    -p "${port}:5432" \
    -v "${volume}:/var/lib/postgresql/data" \
    postgres:18.3 >/dev/null

wait_for_log_count 2 || fail "postgres did not become ready"

url="postgres://erun:testpass@127.0.0.1:${port}/erun?sslmode=disable"
(cd "${db_module}" && ERUN_DATABASE_URL="${url}" sh -c '
    set -eu
    if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-grants-test-apply.log; then
        cat /tmp/erun-grants-test-apply.log >&2
        exit 1
    fi
')

# --- Shared fixtures every per-table assertion below can FK against ---
psql_as -d erun <<'SQL'
INSERT INTO tenants (tenant_id, name) VALUES
  ('00000000-0000-0000-0000-000000000001', 'grants-test-tenant');
INSERT INTO users (user_id, tenant_id, username) VALUES
  ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'grants-test-user');
INSERT INTO environments (tenant_id, environment_id, name, type) VALUES
  ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'grants-test-env', 'local-agent');
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'grants-test-review', 'main', 'feat-grants', 'OPEN');
SQL

# assert_delete_ok <name> <insert-sql> <delete-sql> [row-identity-where]
# Runs insert then delete under erun_operations inside one transaction that
# rolls back, so no assertion below depends on ordering or leaves state for
# the next one. A permission-denied error surfaces as a nonzero psql exit
# under ON_ERROR_STOP, which fail() below reports.
assert_delete_ok() {
    name="$1"
    body="$2"
    log="/tmp/erun-grants-test-${name}.log"
    if ! psql_as -d erun >"${log}" 2>&1 <<SQL
BEGIN;
SET LOCAL ROLE erun_operations;
${body}
ROLLBACK;
SQL
    then
        cat "${log}" >&2
        fail "erun_operations must be able to INSERT then DELETE a ${name} row (retention policy would fail at runtime otherwise)"
    fi
}

# assert_delete_denied <name> <insert-sql> <delete-sql>
# The append-only tables: insert must succeed (the role keeps its normal
# write access), but the delete must fail with a permission error --
# confirming the deliberate #1959 carve-out is still in force, not silently
# widened.
assert_delete_denied() {
    name="$1"
    insert_body="$2"
    delete_body="$3"
    log="/tmp/erun-grants-test-${name}.log"
    if ! psql_as -d erun >"${log}" 2>&1 <<SQL
BEGIN;
SET LOCAL ROLE erun_operations;
${insert_body}
ROLLBACK;
SQL
    then
        cat "${log}" >&2
        fail "erun_operations must still be able to INSERT a ${name} row"
    fi

    log="/tmp/erun-grants-test-${name}-delete.log"
    if psql_as -d erun >"${log}" 2>&1 <<SQL
BEGIN;
SET LOCAL ROLE erun_operations;
${delete_body}
ROLLBACK;
SQL
    then
        fail "erun_operations must NOT be able to DELETE from ${name} -- #1959's append-only carve-out has regressed"
    fi
    grep -qi "permission denied" "${log}" ||
        fail "the ${name} delete must be refused with a permission error, got: $(cat "${log}")"
}

# --- The #1968 six-table sweep: comments, releases, reviews, ai_sessions,
#     invites, invite_requests ---

assert_delete_ok "comments" "
INSERT INTO comments (tenant_id, review_id, creator_user_id, status, commit_id, file_path, line, body)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000002', 'OPEN', 'c1', 'f.go', 1, 'grants test comment');
DELETE FROM comments WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND file_path = 'f.go';
"

assert_delete_ok "releases" "
INSERT INTO releases (tenant_id, target_branch, commit_id, status)
VALUES ('00000000-0000-0000-0000-000000000001', 'main', 'rel-commit-1', 'queued');
DELETE FROM releases WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND commit_id = 'rel-commit-1';
"

assert_delete_ok "reviews" "
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status)
VALUES ('00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'grants-test-review-disposable', 'main', 'feat-grants-disposable', 'OPEN');
DELETE FROM reviews WHERE review_id = '00000000-0000-0000-0000-000000000005';
"

assert_delete_ok "ai_sessions" "
INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'grants-test-session', 'exit', now());
DELETE FROM ai_sessions WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND session_id = 'grants-test-session';
"

assert_delete_ok "invites" "
INSERT INTO invites (invite_id, tenant_id, created_by_user_id, issuer, token, expires_at)
VALUES ('00000000-0000-0000-0000-000000000006', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'https://issuer.example', 'grants-test-token', now() + interval '7 days');
DELETE FROM invites WHERE invite_id = '00000000-0000-0000-0000-000000000006';
"

assert_delete_ok "invite_requests" "
INSERT INTO invite_requests (invite_request_id, issuer, subject, kind, tenant_name, status)
VALUES ('00000000-0000-0000-0000-000000000007', 'https://issuer.example', 'grants-test-subject', 'JOIN_TENANT', 'grants-test-tenant', 'PENDING');
DELETE FROM invite_requests WHERE invite_request_id = '00000000-0000-0000-0000-000000000007';
"

# --- Predating the sweep: builds, gate_runs (#1956) ---

assert_delete_ok "builds" "
INSERT INTO builds (build_id, tenant_id, environment_id, kind, successful, commit_id, version)
VALUES ('00000000-0000-0000-0000-000000000008', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'RECORDED', true, 'build-commit-1', 'v0.0.1');
DELETE FROM builds WHERE build_id = '00000000-0000-0000-0000-000000000008';
"

assert_delete_ok "gate_runs" "
INSERT INTO gate_runs (gate_run_id, tenant_id, source_branch, target_branch, source_commit, merge_commit, status)
VALUES ('00000000-0000-0000-0000-000000000009', '00000000-0000-0000-0000-000000000001', 'feat-grants', 'main', 'src-commit-1', 'merge-commit-1', 'RUNNING');
DELETE FROM gate_runs WHERE gate_run_id = '00000000-0000-0000-0000-000000000009';
"

# --- Predating the sweep, deliberately append-only: audit_events,
#     usage_events (#1959) ---

assert_delete_denied "audit_events" "
INSERT INTO audit_events (tenant_id, erun_user_id, external_user_id, external_issuer_id, type, api_method, api_path)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'ext-user-1', 'https://issuer.example', 'API', 'GET', '/v1/grants-test');
" "
DELETE FROM audit_events WHERE tenant_id = '00000000-0000-0000-0000-000000000001';
"

assert_delete_denied "usage_events" "
INSERT INTO usage_events (tenant_id, environment_id, event_type)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'environment_provisioned');
" "
DELETE FROM usage_events WHERE tenant_id = '00000000-0000-0000-0000-000000000001';
"

echo "OK: erun_operations can actually INSERT then DELETE a row in every table under an implemented or designed retention policy (comments, releases, reviews, ai_sessions, invites, invite_requests, builds, gate_runs), and remains refused on audit_events/usage_events per #1959's append-only carve-out"
