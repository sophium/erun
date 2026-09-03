#!/bin/sh

# End-to-end proof that the reviews retention sweep
# (erun-backend-db/retention/reviews.sql) deletes exactly the rows the
# design in erun-backend-db/AGENTS.md ("Reviews") says it should, and
# nothing else -- against a real postgres:18.3, not a mock. Reviews has six
# inbound foreign keys (five RESTRICT: comments, releases, review_reviewers,
# review_merge_queue, gate_runs, plus builds), so this locks the referential
# order directly rather than trusting the design's own guard list, which
# names comments/review_reviewers/releases/gate_runs but omits builds --
# see reviews.sql's header comment for why builds needs its own guard too.
#
# Locks these properties directly against persisted database state:
#
#   1. a CLOSED review past the 90-day age bound with no referencing row in
#      any of comments/releases/builds/gate_runs/review_merge_queue is
#      deleted together with its review_reviewers rows.
#   2. a CLOSED review past the age bound with an unpinned builds row still
#      pointing at it (builds.review_id, no last_*_build_id pin) survives --
#      the gap this file's own guard closes.
#   3. a CLOSED review past the age bound with a stale last_failed_build_id
#      pin survives (still guarded by the same builds row), but the pin
#      itself is nulled by the sweep; the build row is untouched.
#   4. a CLOSED review past the age bound referenced by a comment, a
#      release, a gate_runs row, or a review_merge_queue row each survives.
#   5. an OPEN review, however old, is exempt (only CLOSED is eligible).
#   6. a MERGED review, however old, is exempt entirely, and its pinned
#      build survives too.
#   7. a CLOSED review within the 90-day age bound survives.
#   8. CLOSED reviews with no referencing rows beyond the
#      2,000-most-recent-per-tenant count cap are deleted even when every
#      row is well within the age bound.
#
# It also proves dry_run=true reports the same counts but deletes and
# nulls nothing.
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

container="erun-backend-db-reviews-retention-test-$$"
volume="erun-backend-db-reviews-retention-test-$$"
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
    if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-reviews-retention-test-apply.log; then
        cat /tmp/erun-reviews-retention-test-apply.log >&2
        exit 1
    fi
')

# --- Fixtures ---
# Tenants 1-9 each host one review exercising one guard/exemption; tenant 10
# hosts the count-cap population. All reviews needing an old updated_at are
# inserted directly with an explicit old created_at/updated_at (INSERT lets
# a caller supply both); the two scenarios that need a mutation after insert
# (the stale-pin and MERGED cases) temporarily disable reviews' own
# timestamp trigger to backdate updated_at, since a normal UPDATE always
# refreshes it to now() -- exactly the property that makes CLOSED itself a
# safe age signal in production.
psql_as -d erun <<'SQL'
INSERT INTO tenants (tenant_id, name) VALUES
  ('00000000-0000-0000-0000-000000000001', 'clean'),
  ('00000000-0000-0000-0000-000000000002', 'builds-unpinned'),
  ('00000000-0000-0000-0000-000000000003', 'builds-pinned'),
  ('00000000-0000-0000-0000-000000000004', 'comments-guard'),
  ('00000000-0000-0000-0000-000000000005', 'releases-guard'),
  ('00000000-0000-0000-0000-000000000006', 'gate-runs-guard'),
  ('00000000-0000-0000-0000-000000000007', 'queue-guard'),
  ('00000000-0000-0000-0000-000000000008', 'open'),
  ('00000000-0000-0000-0000-000000000009', 'merged'),
  ('00000000-0000-0000-0000-00000000000a', 'young'),
  ('00000000-0000-0000-0000-00000000000b', 'count-cap');

INSERT INTO users (user_id, tenant_id, username) VALUES
  ('00000000-0000-0000-0000-100000000001', '00000000-0000-0000-0000-000000000001', 'u1'),
  ('00000000-0000-0000-0000-100000000002', '00000000-0000-0000-0000-000000000002', 'u2'),
  ('00000000-0000-0000-0000-100000000003', '00000000-0000-0000-0000-000000000003', 'u3'),
  ('00000000-0000-0000-0000-100000000004', '00000000-0000-0000-0000-000000000004', 'u4'),
  ('00000000-0000-0000-0000-100000000005', '00000000-0000-0000-0000-000000000005', 'u5'),
  ('00000000-0000-0000-0000-100000000006', '00000000-0000-0000-0000-000000000006', 'u6'),
  ('00000000-0000-0000-0000-100000000007', '00000000-0000-0000-0000-000000000007', 'u7'),
  ('00000000-0000-0000-0000-100000000008', '00000000-0000-0000-0000-000000000008', 'u8'),
  ('00000000-0000-0000-0000-100000000009', '00000000-0000-0000-0000-000000000009', 'u9'),
  ('00000000-0000-0000-0000-10000000000a', '00000000-0000-0000-0000-00000000000a', 'ua'),
  ('00000000-0000-0000-0000-10000000000b', '00000000-0000-0000-0000-00000000000b', 'ub');

-- 1: clean -- CLOSED, 200 days old, no referencing rows.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-100000000001', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO review_reviewers (tenant_id, review_id, user_id) VALUES
  ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-100000000001');

-- 2: builds-unpinned -- CLOSED, 200 days old, an unpinned successful GATE
-- build still references it via builds.review_id alone.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-100000000002', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO review_reviewers (tenant_id, review_id, user_id) VALUES
  ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-100000000002');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version) VALUES
  ('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000201', 'GATE', true, 'c', NULL);

-- 3: builds-pinned -- starts OPEN so a FAILED GATE build can reference it,
-- then backdated to CLOSED with the build pinned as last_failed_build_id.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-100000000003', 'r', 'main', 'feat', 'OPEN');
INSERT INTO review_reviewers (tenant_id, review_id, user_id) VALUES
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-100000000003');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version, failure_detail) VALUES
  ('00000000-0000-0000-0000-000000000302', '00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000301', 'GATE', false, 'c', NULL, 'gate failed');
ALTER TABLE reviews DISABLE TRIGGER reviews_set_timestamps;
UPDATE reviews SET status = 'CLOSED', last_failed_build_id = '00000000-0000-0000-0000-000000000302',
       created_at = now() - interval '200 days', updated_at = now() - interval '200 days'
  WHERE review_id = '00000000-0000-0000-0000-000000000301';
ALTER TABLE reviews ENABLE TRIGGER reviews_set_timestamps;

-- 4: comments-guard -- CLOSED, 200 days old, referenced by a comment.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000401', '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-100000000004', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO comments (tenant_id, review_id, creator_user_id, status, commit_id, file_path, line, body) VALUES
  ('00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000401', '00000000-0000-0000-0000-100000000004', 'CLOSED', 'c', 'f', 1, 'still on the books');

-- 5: releases-guard -- CLOSED, 200 days old, referenced by a release.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000501', '00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-100000000005', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO releases (tenant_id, review_id, target_branch, commit_id, status, version) VALUES
  ('00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000501', 'main', 'c', 'released', 'v1');

-- 6: gate-runs-guard -- CLOSED, 200 days old, referenced by a gate_runs row.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000006', '00000000-0000-0000-0000-100000000006', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO gate_runs (tenant_id, review_id, source_branch, target_branch, source_commit, merge_commit, status) VALUES
  ('00000000-0000-0000-0000-000000000006', '00000000-0000-0000-0000-000000000601', 'feat', 'main', 'c', 'c', 'PASSED');

-- 7: queue-guard -- CLOSED, 200 days old, referenced by a review_merge_queue
-- row (defensive: this shouldn't happen per this file's own documented
-- invariant, but the guard must catch it if it ever does).
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000701', '00000000-0000-0000-0000-000000000007', '00000000-0000-0000-0000-100000000007', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO review_merge_queue (tenant_id, target_branch, review_id) VALUES
  ('00000000-0000-0000-0000-000000000007', 'main', '00000000-0000-0000-0000-000000000701');

-- 8: open -- OPEN, 200 days old, no referencing rows. Never eligible: only
-- CLOSED is a candidate status.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000801', '00000000-0000-0000-0000-000000000008', '00000000-0000-0000-0000-100000000008', 'r', 'main', 'feat', 'OPEN', now() - interval '200 days', now() - interval '200 days');

-- 9: merged -- starts OPEN so a successful GATE build can reference it,
-- then backdated to MERGED with the build pinned as last_merged_build_id.
-- Exempt entirely regardless of age.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('00000000-0000-0000-0000-000000000901', '00000000-0000-0000-0000-000000000009', '00000000-0000-0000-0000-100000000009', 'r', 'main', 'feat', 'OPEN');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version) VALUES
  ('00000000-0000-0000-0000-000000000902', '00000000-0000-0000-0000-000000000009', '00000000-0000-0000-0000-000000000901', 'GATE', true, 'c', NULL);
ALTER TABLE reviews DISABLE TRIGGER reviews_set_timestamps;
UPDATE reviews SET status = 'MERGED', last_merged_build_id = '00000000-0000-0000-0000-000000000902',
       created_at = now() - interval '200 days', updated_at = now() - interval '200 days'
  WHERE review_id = '00000000-0000-0000-0000-000000000901';
ALTER TABLE reviews ENABLE TRIGGER reviews_set_timestamps;

-- 10 (tenant a): young -- CLOSED, 5 days old, no referencing rows. Within
-- the 90-day age bound, survives.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000a01', '00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-10000000000a', 'r', 'main', 'feat', 'CLOSED', now() - interval '5 days', now() - interval '5 days');

-- 11 (tenant b): count-cap -- 2005 CLOSED reviews, no referencing rows,
-- seconds apart. Only the 5 oldest (beyond the 2,000-row cap) are eligible.
INSERT INTO reviews (tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at)
SELECT '00000000-0000-0000-0000-00000000000b', '00000000-0000-0000-0000-10000000000b', 'r-' || i, 'main', 'feat-' || i, 'CLOSED',
       now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 2005) AS i;
SQL

initial_reviews="$(count_query "select count(*) from reviews;")"
initial_reviewers="$(count_query "select count(*) from review_reviewers;")"
[ "${initial_reviews}" = "2015" ] || fail "expected 2015 fixture reviews (10 + 2005 count-cap), got ${initial_reviews}"
[ "${initial_reviewers}" = "3" ] || fail "expected 3 fixture review_reviewers (clean, builds-unpinned, builds-pinned), got ${initial_reviewers}"

# --- Dry run reports the exact eligible counts and mutates nothing ---
report="$(docker cp "${db_module}/retention/reviews.sql" "${container}:/tmp/reviews.sql" &&
    psql_as -d erun -v "dry_run=true" -f /tmp/reviews.sql)"
printf '%s\n' "${report}" | grep -E '^\s*reviews\s*\|\s*6\s*$' >/dev/null ||
    fail "dry run must report 6 reviews eligible (1 clean + 5 count-cap), got:\n${report}"
printf '%s\n' "${report}" | grep -E '^\s*review_reviewers\s*\|\s*1\s*$' >/dev/null ||
    fail "dry run must report 1 review_reviewers eligible (the clean review's), got:\n${report}"

[ "$(count_query "select count(*) from reviews;")" = "${initial_reviews}" ] || fail "dry run must not delete any reviews"
[ "$(count_query "select count(*) from review_reviewers;")" = "${initial_reviewers}" ] || fail "dry run must not delete any review_reviewers"
[ "$(count_query "select last_failed_build_id is null from reviews where review_id = '00000000-0000-0000-0000-000000000301';")" = "f" ] ||
    fail "dry run must not null the stale pin either"

# --- Real run deletes/nulls exactly the eligible rows ---
psql_as -d erun -v "dry_run=false" -f /tmp/reviews.sql >/dev/null

# 1: clean is gone, its reviewer is gone.
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000101';")" = "0" ] ||
    fail "the clean CLOSED review must be deleted"
[ "$(count_query "select count(*) from review_reviewers where review_id = '00000000-0000-0000-0000-000000000101';")" = "0" ] ||
    fail "the clean review's reviewer must be deleted with it"

# 2: builds-unpinned survives -- the unpinned builds row is the guard this
# file's own gap-fix closes.
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000201';")" = "1" ] ||
    fail "a CLOSED review with an unpinned builds row must survive"
[ "$(count_query "select count(*) from review_reviewers where review_id = '00000000-0000-0000-0000-000000000201';")" = "1" ] ||
    fail "its reviewer must survive too"
[ "$(count_query "select count(*) from builds where build_id = '00000000-0000-0000-0000-000000000202';")" = "1" ] ||
    fail "the unpinned build itself must be untouched"

# 3: builds-pinned survives (still guarded by the same build), its reviewer
# survives, the build is untouched, but the stale pin is nulled.
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000301';")" = "1" ] ||
    fail "a CLOSED review with a pinned builds row must survive"
[ "$(count_query "select count(*) from review_reviewers where review_id = '00000000-0000-0000-0000-000000000301';")" = "1" ] ||
    fail "its reviewer must survive too"
[ "$(count_query "select last_failed_build_id from reviews where review_id = '00000000-0000-0000-0000-000000000301';")" = "" ] ||
    fail "the stale last_failed_build_id pin must be nulled"
[ "$(count_query "select count(*) from builds where build_id = '00000000-0000-0000-0000-000000000302';")" = "1" ] ||
    fail "the pinned build itself must survive -- this policy never touches builds"

# 4-7: each guard keeps its review alive along with the referencing row.
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000401';")" = "1" ] ||
    fail "a CLOSED review referenced by a comment must survive"
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000501';")" = "1" ] ||
    fail "a CLOSED review referenced by a release must survive"
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000601';")" = "1" ] ||
    fail "a CLOSED review referenced by a gate_runs row must survive"
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000701';")" = "1" ] ||
    fail "a CLOSED review referenced by a review_merge_queue row must survive"

# 8: OPEN survives no matter how old.
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000801';")" = "1" ] ||
    fail "an OPEN review must survive no matter how old it is"

# 9: MERGED survives entirely, its pinned build survives too.
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000901';")" = "1" ] ||
    fail "a MERGED review must survive no matter how old it is"
[ "$(count_query "select count(*) from builds where build_id = '00000000-0000-0000-0000-000000000902';")" = "1" ] ||
    fail "a MERGED review's pinned build must survive"

# 10: young survives (within the 90-day age bound).
[ "$(count_query "select count(*) from reviews where review_id = '00000000-0000-0000-0000-000000000a01';")" = "1" ] ||
    fail "a CLOSED review within the age bound must survive"

# 11: count-cap tenant keeps exactly 2000, the 5 oldest are gone.
[ "$(count_query "select count(*) from reviews where tenant_id = '00000000-0000-0000-0000-00000000000b';")" = "2000" ] ||
    fail "the count-cap tenant must keep exactly 2000 reviews"
[ "$(count_query "select count(*) from reviews where tenant_id = '00000000-0000-0000-0000-00000000000b' and name in ('r-2001','r-2002','r-2003','r-2004','r-2005');")" = "0" ] ||
    fail "the count-cap tenant's 5 oldest reviews must be deleted"
[ "$(count_query "select count(*) from reviews where tenant_id = '00000000-0000-0000-0000-00000000000b' and name = 'r-1';")" = "1" ] ||
    fail "the count-cap tenant's newest review must survive"

echo "OK: reviews retention deletes exactly the age- and count-bound, fully-unreferenced CLOSED reviews the design specifies, nulls stale pins on blocked ones, and touches nothing else"
