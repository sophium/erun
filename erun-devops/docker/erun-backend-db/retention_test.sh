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
# It also proves dry_run=true reports the same counts but deletes nothing.
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

i=0
while [ "$i" -lt 60 ]; do
    docker exec "${container}" pg_isready -U erun -d erun >/dev/null 2>&1 && break
    i=$((i + 1))
    sleep 1
done
docker exec "${container}" pg_isready -U erun -d erun >/dev/null 2>&1 || fail "postgres did not become ready"

url="postgres://erun:testpass@localhost:${port}/erun?sslmode=disable"
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

# --- Real run deletes exactly the eligible rows ---
psql_as -d erun -v "dry_run=false" -f /tmp/comments_releases.sql >/dev/null

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

echo "OK: comments/releases retention deletes exactly the age- and count-bound rows the design specifies, and nothing else"
