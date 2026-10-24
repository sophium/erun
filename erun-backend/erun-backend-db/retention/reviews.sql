-- Retention sweep for CLOSED reviews; see erun-backend-db/AGENTS.md, "Reviews".
-- MERGED reviews are exempt: unbounded branch history may be a product promise.
-- Status is exclusive (reviews_status_check), so status = 'CLOSED' never matches a merged review.
-- Reviews still referenced by builds, comments, releases or the merge queue (RESTRICT FKs)
-- are skipped, so few qualify until builds have their own retention.
-- review_reviewers has no other cleanup path, so it is pruned here with its review.
-- The 90-day / 2,000-per-tenant bounds are starting values; tune from real counts.
-- Requires psql variable dry_run (true|false).

SET ROLE erun_operations;

WITH closed_ranked AS (
  SELECT review_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM reviews
  WHERE status = 'CLOSED'
),
age_count_eligible AS (
  SELECT review_id, tenant_id FROM closed_ranked
  WHERE rn > 2000 OR updated_at < now() - interval '90 days'
),
guarded_eligible AS (
  SELECT e.review_id, e.tenant_id
  FROM age_count_eligible e
  WHERE NOT EXISTS (SELECT 1 FROM comments c WHERE c.tenant_id = e.tenant_id AND c.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM releases rel WHERE rel.tenant_id = e.tenant_id AND rel.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM builds b WHERE b.tenant_id = e.tenant_id AND b.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
SELECT 'reviews' AS table_name, count(*) AS eligible_for_deletion FROM guarded_eligible;

WITH closed_ranked AS (
  SELECT review_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM reviews
  WHERE status = 'CLOSED'
),
age_count_eligible AS (
  SELECT review_id, tenant_id FROM closed_ranked
  WHERE rn > 2000 OR updated_at < now() - interval '90 days'
),
guarded_eligible AS (
  SELECT e.review_id, e.tenant_id
  FROM age_count_eligible e
  WHERE NOT EXISTS (SELECT 1 FROM comments c WHERE c.tenant_id = e.tenant_id AND c.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM releases rel WHERE rel.tenant_id = e.tenant_id AND rel.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM builds b WHERE b.tenant_id = e.tenant_id AND b.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
SELECT 'review_reviewers' AS table_name, count(*) AS eligible_for_deletion
FROM review_reviewers rr
WHERE (rr.tenant_id, rr.review_id) IN (SELECT tenant_id, review_id FROM guarded_eligible);

-- deleted_count equals eligible_count on a real run: the delete repeats this predicate under
-- retention.sh's sweep-wide advisory lock.
WITH closed_ranked AS (
  SELECT review_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM reviews
  WHERE status = 'CLOSED'
),
age_count_eligible AS (
  SELECT review_id, tenant_id FROM closed_ranked
  WHERE rn > 2000 OR updated_at < now() - interval '90 days'
),
guarded_eligible AS (
  SELECT e.review_id, e.tenant_id
  FROM age_count_eligible e
  WHERE NOT EXISTS (SELECT 1 FROM comments c WHERE c.tenant_id = e.tenant_id AND c.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM releases rel WHERE rel.tenant_id = e.tenant_id AND rel.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM builds b WHERE b.tenant_id = e.tenant_id AND b.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
),
reviews_eligible AS (
  SELECT count(*) AS n FROM guarded_eligible
),
review_reviewers_eligible AS (
  SELECT count(*) AS n
  FROM review_reviewers rr
  WHERE (rr.tenant_id, rr.review_id) IN (SELECT tenant_id, review_id FROM guarded_eligible)
)
INSERT INTO retention_runs (policy_name, table_name, dry_run, eligible_count, deleted_count)
SELECT 'reviews', 'reviews', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM reviews_eligible
UNION ALL
SELECT 'reviews', 'review_reviewers', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM review_reviewers_eligible;

\if :dry_run
\else
BEGIN;

-- Unpin builds on every review past the bound, guarded or not, so a future builds sweep
-- can prune them.
WITH closed_ranked AS (
  SELECT review_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM reviews
  WHERE status = 'CLOSED'
),
age_count_eligible AS (
  SELECT review_id, tenant_id FROM closed_ranked
  WHERE rn > 2000 OR updated_at < now() - interval '90 days'
)
UPDATE reviews r
SET last_failed_build_id = NULL, last_ready_build_id = NULL
WHERE (r.tenant_id, r.review_id) IN (SELECT tenant_id, review_id FROM age_count_eligible)
  AND (r.last_failed_build_id IS NOT NULL OR r.last_ready_build_id IS NOT NULL);

WITH closed_ranked AS (
  SELECT review_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM reviews
  WHERE status = 'CLOSED'
),
age_count_eligible AS (
  SELECT review_id, tenant_id FROM closed_ranked
  WHERE rn > 2000 OR updated_at < now() - interval '90 days'
),
guarded_eligible AS (
  SELECT e.review_id, e.tenant_id
  FROM age_count_eligible e
  WHERE NOT EXISTS (SELECT 1 FROM comments c WHERE c.tenant_id = e.tenant_id AND c.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM releases rel WHERE rel.tenant_id = e.tenant_id AND rel.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM builds b WHERE b.tenant_id = e.tenant_id AND b.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
-- Before reviews, in the same transaction, so a reviewer row only goes with its review.
DELETE FROM review_reviewers rr
WHERE (rr.tenant_id, rr.review_id) IN (SELECT tenant_id, review_id FROM guarded_eligible);

WITH closed_ranked AS (
  SELECT review_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM reviews
  WHERE status = 'CLOSED'
),
age_count_eligible AS (
  SELECT review_id, tenant_id FROM closed_ranked
  WHERE rn > 2000 OR updated_at < now() - interval '90 days'
),
guarded_eligible AS (
  SELECT e.review_id, e.tenant_id
  FROM age_count_eligible e
  WHERE NOT EXISTS (SELECT 1 FROM comments c WHERE c.tenant_id = e.tenant_id AND c.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM releases rel WHERE rel.tenant_id = e.tenant_id AND rel.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM builds b WHERE b.tenant_id = e.tenant_id AND b.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
DELETE FROM reviews r
WHERE (r.tenant_id, r.review_id) IN (SELECT tenant_id, review_id FROM guarded_eligible);

COMMIT;
\endif
