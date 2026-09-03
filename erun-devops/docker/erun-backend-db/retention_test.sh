#!/bin/sh

# End-to-end proof that the comments/releases retention sweep
# (erun-backend-db/retention/comments_releases.sql) deletes exactly the rows
# the design in erun-backend-db/AGENTS.md ("Review Comments And Releases")
# says it should, and nothing else -- against a real postgres:18.3, not a
# mock. Locks four properties directly against persisted database state:
#
#   1. releases past the 180-day age bound are deleted; releases within it
#      survive.
#   2. releases beyond the 1,000-most-recent-per-tenant count cap are
#      deleted even when every row is well within the age bound.
#   3. comments: an OPEN root thread is exempt from the 30-day age bound no
#      matter how old it is; a CLOSED root thread past the bound is deleted
#      together with every reply on it, even a reply younger than the
#      bound; a CLOSED root thread within the bound survives.
#   4. comments beyond the 5,000-most-recent-closed-root-per-tenant count
#      cap are deleted even when every row is well within the age bound.
#
# It also proves dry_run=true reports the same counts but deletes nothing,
# that both runs record their outcome in retention_runs (eligible_count and
# deleted_count, tagged with dry_run), and -- driving the real
# retention.sh entrypoint rather than psql directly -- that a second run
# started while one is already in flight is refused by the session-scoped
# advisory lock, and that a plain invocation with no ERUN_RETENTION_DRY_RUN
# set defaults to report-only.
#
# Lives beside migrate_test.sh and follows the same shape: a real docker
# daemon runs postgres, the real migrations from the sibling erun-backend-db
# module are applied via the atlas CLI (not the built image, to avoid a full
# image build just to prove the SQL), and the retention SQL is run directly
# via psql against the exposed port -- the same "test the real command
# against a real database, not a mock" reasoning migrate_test.sh's own
# header explains. Run this via `make test-retention` -- by hand, or via
# `erun exec job` in an agent env -- rather than `make check`, which has
# neither a docker daemon nor the atlas CLI available inside its bare test
# image (same constraint as test-postgres-restart).

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

container="erun-backend-db-retention-test-$$"
volume="erun-backend-db-retention-test-$$"
port=15433
work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-backend-db-retention-test)"

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    docker volume rm "${volume}" >/dev/null 2>&1 || true
    rm -rf "${work_root}"
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
# gap. Waiting for the line to appear twice in the container's own log is
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

wait_for_log_count 2 || fail "postgres did not become ready"

# 127.0.0.1, not localhost: docker's port publish binds IPv4 only, and this
# host's resolver sometimes prefers localhost's IPv6 (::1) address, which
# gets a connection reset rather than falling back to IPv4.
url="postgres://erun:testpass@127.0.0.1:${port}/erun?sslmode=disable"
(cd "${db_module}" && ERUN_DATABASE_URL="${url}" sh -c '
    set -eu
    if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-retention-test-apply.log; then
        cat /tmp/erun-retention-test-apply.log >&2
        exit 1
    fi
')

# --- Fixtures ---
# releases-age-tenant (RA): one release 200 days old (past the 180-day
# bound, eligible), one release 10 days old (within it, survives).
# releases-count-tenant (RC): 1005 releases, all seconds old (nowhere near
# the age bound), spaced one second apart so ranking by created_at is
# deterministic. Only the 5 oldest (beyond the 1,000-row cap) are eligible.
# comments-age-tenant (CA): an OPEN root 200 days old (exempt regardless of
# age), a CLOSED root 200 days old with a reply posted moments ago (the
# whole thread is eligible, including the young reply), and a CLOSED root
# 5 days old (within the 30-day bound, survives).
# comments-count-tenant (CC): 5005 CLOSED root threads with no replies, all
# seconds old, spaced one second apart. Only the 5 oldest (beyond the
# 5,000-row cap) are eligible.
psql_as -d erun <<'SQL'
INSERT INTO tenants (tenant_id, name) VALUES
  ('00000000-0000-0000-0000-000000000001', 'releases-age-tenant'),
  ('00000000-0000-0000-0000-000000000002', 'releases-count-tenant'),
  ('00000000-0000-0000-0000-000000000003', 'comments-age-tenant'),
  ('00000000-0000-0000-0000-000000000004', 'comments-count-tenant');

INSERT INTO users (user_id, tenant_id, username) VALUES
  ('00000000-0000-0000-0000-000000000103', '00000000-0000-0000-0000-000000000003', 'ca-user'),
  ('00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-000000000004', 'cc-user');

INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000103', 'ca-review', 'main', 'feat-ca', 'OPEN'),
  ('00000000-0000-0000-0000-000000000204', '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000104', 'cc-review', 'main', 'feat-cc', 'OPEN');

INSERT INTO releases (tenant_id, target_branch, commit_id, status, version, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000001', 'main', 'ra-old', 'released', 'v1.0.0', now() - interval '200 days', now() - interval '200 days'),
  ('00000000-0000-0000-0000-000000000001', 'main', 'ra-young', 'released', 'v1.0.1', now() - interval '10 days', now() - interval '10 days');

INSERT INTO releases (tenant_id, target_branch, commit_id, status, version, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000002', 'main', 'rc-' || i, 'released', 'v0.0.' || i,
       now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 1005) AS i;

INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'OPEN', NULL, 'c', 'ca-open-root', 1, 'open root, never ages out', now() - interval '200 days', now() - interval '200 days'),
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'CLOSED', NULL, 'c', 'ca-closed-old-root', 1, 'closed root, past the age bound', now() - interval '200 days', now() - interval '200 days'),
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'CLOSED', NULL, 'c', 'ca-closed-young-root', 1, 'closed root, within the age bound', now() - interval '5 days', now() - interval '5 days');

INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'OPEN', comment_id, 'c', 'ca-closed-old-root', 1, 'reply posted moments ago, root is old', now(), now()
FROM comments WHERE tenant_id = '00000000-0000-0000-0000-000000000003' AND file_path = 'ca-closed-old-root' AND parent_comment_id IS NULL;

INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000204', '00000000-0000-0000-0000-000000000104', 'CLOSED', NULL, 'c', 'cc-' || i, 1, 'closed root ' || i,
       now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 5005) AS i;
SQL

initial_releases="$(count_query "select count(*) from releases;")"
initial_comments="$(count_query "select count(*) from comments;")"
[ "${initial_releases}" = "1007" ] || fail "expected 1007 fixture releases (2 + 1005), got ${initial_releases}"
[ "${initial_comments}" = "5009" ] || fail "expected 5009 fixture comments (3 roots + 1 reply + 5005 roots), got ${initial_comments}"

# --- Dry run reports the exact eligible counts and deletes nothing ---
report="$(docker cp "${db_module}/retention/comments_releases.sql" "${container}:/tmp/comments_releases.sql" &&
    psql_as -d erun -v "dry_run=true" -f /tmp/comments_releases.sql)"
printf '%s\n' "${report}" | grep -E '^\s*releases\s*\|\s*6\s*$' >/dev/null ||
    fail "dry run must report 6 releases eligible across all tenants (1 age-bound + 5 count-cap), got:\n${report}"
printf '%s\n' "${report}" | grep -E '^\s*comments\s*\|\s*7\s*$' >/dev/null ||
    fail "dry run must report 7 comments eligible across all tenants (the old closed root + its young reply, + 5 count-cap roots), got:\n${report}"

[ "$(count_query "select count(*) from releases;")" = "${initial_releases}" ] || fail "dry run must not delete any releases"
[ "$(count_query "select count(*) from comments;")" = "${initial_comments}" ] || fail "dry run must not delete any comments"

# --- The dry run recorded its outcome: eligible counts, zero deleted ---
[ "$(count_query "select eligible_count from retention_runs where policy_name='comments_releases' and table_name='releases' and dry_run=true order by created_at desc limit 1;")" = "6" ] ||
    fail "the dry run must record 6 releases eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='comments_releases' and table_name='releases' and dry_run=true order by created_at desc limit 1;")" = "0" ] ||
    fail "the dry run must record 0 releases deleted in retention_runs"
[ "$(count_query "select eligible_count from retention_runs where policy_name='comments_releases' and table_name='comments' and dry_run=true order by created_at desc limit 1;")" = "7" ] ||
    fail "the dry run must record 7 comments eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='comments_releases' and table_name='comments' and dry_run=true order by created_at desc limit 1;")" = "0" ] ||
    fail "the dry run must record 0 comments deleted in retention_runs"

# --- Real run deletes exactly the eligible rows ---
psql_as -d erun -v "dry_run=false" -f /tmp/comments_releases.sql >/dev/null

# --- The real run recorded its outcome: eligible counts, matching deleted counts ---
[ "$(count_query "select eligible_count from retention_runs where policy_name='comments_releases' and table_name='releases' and dry_run=false order by created_at desc limit 1;")" = "6" ] ||
    fail "the real run must record 6 releases eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='comments_releases' and table_name='releases' and dry_run=false order by created_at desc limit 1;")" = "6" ] ||
    fail "the real run must record 6 releases deleted in retention_runs"
[ "$(count_query "select eligible_count from retention_runs where policy_name='comments_releases' and table_name='comments' and dry_run=false order by created_at desc limit 1;")" = "7" ] ||
    fail "the real run must record 7 comments eligible in retention_runs"
[ "$(count_query "select deleted_count from retention_runs where policy_name='comments_releases' and table_name='comments' and dry_run=false order by created_at desc limit 1;")" = "7" ] ||
    fail "the real run must record 7 comments deleted in retention_runs"

# 1. releases-age-tenant: the 200-day-old row is gone, the 10-day-old row survives.
[ "$(count_query "select count(*) from releases where tenant_id = '00000000-0000-0000-0000-000000000001' and commit_id = 'ra-old';")" = "0" ] ||
    fail "the age-bound tenant's old release must be deleted"
[ "$(count_query "select count(*) from releases where tenant_id = '00000000-0000-0000-0000-000000000001' and commit_id = 'ra-young';")" = "1" ] ||
    fail "the age-bound tenant's young release must survive"

# 2. releases-count-tenant: exactly 1,000 survive -- the 5 oldest are gone.
[ "$(count_query "select count(*) from releases where tenant_id = '00000000-0000-0000-0000-000000000002';")" = "1000" ] ||
    fail "the count-cap tenant must keep exactly 1000 releases"
[ "$(count_query "select count(*) from releases where tenant_id = '00000000-0000-0000-0000-000000000002' and commit_id in ('rc-1001','rc-1002','rc-1003','rc-1004','rc-1005');")" = "0" ] ||
    fail "the count-cap tenant's 5 oldest releases must be deleted"
[ "$(count_query "select count(*) from releases where tenant_id = '00000000-0000-0000-0000-000000000002' and commit_id = 'rc-1';")" = "1" ] ||
    fail "the count-cap tenant's newest release must survive"

# 3. comments-age-tenant: OPEN root survives regardless of age, CLOSED-old
#    root and its reply are both gone, CLOSED-young root survives.
[ "$(count_query "select count(*) from comments where tenant_id = '00000000-0000-0000-0000-000000000003' and file_path = 'ca-open-root';")" = "1" ] ||
    fail "an OPEN root must survive no matter how old it is"
[ "$(count_query "select count(*) from comments where tenant_id = '00000000-0000-0000-0000-000000000003' and file_path = 'ca-closed-old-root';")" = "0" ] ||
    fail "a CLOSED root past the age bound must be deleted"
[ "$(count_query "select count(*) from comments where parent_comment_id in (select comment_id from comments where tenant_id = '00000000-0000-0000-0000-000000000003' and file_path = 'ca-closed-old-root');")" = "0" ] ||
    fail "a reply on a deleted thread must be deleted too, even if it is younger than the age bound"
[ "$(count_query "select count(*) from comments where tenant_id = '00000000-0000-0000-0000-000000000003' and file_path = 'ca-closed-young-root';")" = "1" ] ||
    fail "a CLOSED root within the age bound must survive"

# 4. comments-count-tenant: exactly 5,000 closed roots survive -- the 5 oldest are gone.
[ "$(count_query "select count(*) from comments where tenant_id = '00000000-0000-0000-0000-000000000004';")" = "5000" ] ||
    fail "the count-cap tenant must keep exactly 5000 comments"
[ "$(count_query "select count(*) from comments where tenant_id = '00000000-0000-0000-0000-000000000004' and file_path in ('cc-5001','cc-5002','cc-5003','cc-5004','cc-5005');")" = "0" ] ||
    fail "the count-cap tenant's 5 oldest closed roots must be deleted"
[ "$(count_query "select count(*) from comments where tenant_id = '00000000-0000-0000-0000-000000000004' and file_path = 'cc-1';")" = "1" ] ||
    fail "the count-cap tenant's newest closed root must survive"

# --- retention.sh itself: default dry_run, and the advisory lock refuses a
#     second run while one is already in flight ---
# Everything above ran comments_releases.sql directly to control fixtures
# precisely; this section drives the real entrypoint (erun-devops/docker/
# erun-backend-db/retention.sh) inside the same postgres container -- it
# only needs psql, which the postgres image already carries, so this needs
# no image build.
docker exec "${container}" mkdir -p /opt/erun-backend-db/retention
docker cp "${script_dir}/retention.sh" "${container}:/usr/local/bin/erun-backend-db-retention"
docker exec "${container}" chmod +x /usr/local/bin/erun-backend-db-retention
docker cp "${db_module}/retention/comments_releases.sql" "${container}:/opt/erun-backend-db/retention/comments_releases.sql"

in_container_url="postgres://erun:testpass@127.0.0.1:5432/erun?sslmode=disable"

# A plain invocation with ERUN_RETENTION_DRY_RUN unset must default to
# report-only -- it must not delete the two releases that survived above.
before_release_count="$(count_query "select count(*) from releases;")"
docker exec -e "ERUN_DATABASE_URL=${in_container_url}" "${container}" erun-backend-db-retention >/dev/null ||
    fail "retention.sh must succeed with no ERUN_RETENTION_DRY_RUN set"
[ "$(count_query "select count(*) from releases;")" = "${before_release_count}" ] ||
    fail "retention.sh with no ERUN_RETENTION_DRY_RUN set must default to dry_run=true and delete nothing"
[ "$(count_query "select dry_run from retention_runs where policy_name='comments_releases' order by created_at desc limit 1;")" = "t" ] ||
    fail "retention.sh's default run must record dry_run=true in retention_runs"

# Hold the advisory lock in a background session (marking readiness via a
# file, rather than a fixed sleep, so the test doesn't race the lock
# acquisition) and confirm a concurrent retention.sh run is refused.
docker exec "${container}" rm -f /tmp/erun-retention-lock-held
holder="${work_root}/hold_lock.sh"
cat >"${holder}" <<'HOLDER'
#!/bin/sh
psql -U erun -d erun -v ON_ERROR_STOP=1 <<'SQL'
SELECT pg_advisory_lock(hashtext('erun_retention'));
\! touch /tmp/erun-retention-lock-held
SELECT pg_sleep(5);
SQL
HOLDER
docker cp "${holder}" "${container}:/tmp/hold_lock.sh"
docker exec "${container}" chmod +x /tmp/hold_lock.sh
docker exec -d -e PGPASSWORD=testpass "${container}" /tmp/hold_lock.sh

i=0
while [ "$i" -lt 30 ]; do
    docker exec "${container}" test -f /tmp/erun-retention-lock-held && break
    i=$((i + 1))
    sleep 1
done
docker exec "${container}" test -f /tmp/erun-retention-lock-held || fail "background session never acquired the advisory lock"

concurrent_log="${work_root}/concurrent.log"
if docker exec -e "ERUN_DATABASE_URL=${in_container_url}" "${container}" erun-backend-db-retention >"${concurrent_log}" 2>&1; then
    fail "retention.sh must refuse to run while the advisory lock is held by another session"
fi
grep -q "advisory lock held" "${concurrent_log}" ||
    fail "retention.sh's refusal must name the advisory lock, got: $(cat "${concurrent_log}")"

# The holder session's own pg_sleep(5) started at (approximately) the point
# it touched the marker file above; sleeping past that bound guarantees its
# session has disconnected and the session-scoped lock has released.
sleep 7
docker exec -e "ERUN_DATABASE_URL=${in_container_url}" "${container}" erun-backend-db-retention >/dev/null ||
    fail "retention.sh must succeed once the advisory lock is released"

echo "OK: comments/releases retention deletes exactly the age- and count-bound rows the design specifies, records the outcome in retention_runs, and the retention.sh entrypoint defaults to dry_run=true and refuses to overlap a run holding the advisory lock"
