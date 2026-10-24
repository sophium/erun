-- Retention sweep for review comments and releases; see erun-backend-db/AGENTS.md,
-- "Review Comments And Releases". Requires psql variable dry_run (true|false).

SET ROLE erun_operations;

-- releases: 180-day age or 1,000-per-tenant cap. Nothing references releases.
WITH releases_ranked AS (
  SELECT release_id, tenant_id, created_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY created_at DESC) AS rn
  FROM releases
)
SELECT 'releases' AS table_name, count(*) AS eligible_for_deletion
FROM releases_ranked
WHERE rn > 1000 OR created_at < now() - interval '180 days';

-- comments: 30-day age (since close) or 5,000-per-tenant cap, CLOSED root threads only;
-- a root deletes with all its replies so no thread is orphaned.
WITH closed_roots AS (
  SELECT comment_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM comments
  WHERE parent_comment_id IS NULL AND status = 'CLOSED'
),
roots_to_delete AS (
  SELECT comment_id FROM closed_roots
  WHERE rn > 5000 OR updated_at < now() - interval '30 days'
)
SELECT 'comments' AS table_name, count(*) AS eligible_for_deletion
FROM comments
WHERE comment_id IN (SELECT comment_id FROM roots_to_delete)
   OR parent_comment_id IN (SELECT comment_id FROM roots_to_delete);

-- deleted_count equals eligible_count on a real run: the delete repeats this predicate under
-- retention.sh's sweep-wide advisory lock.
WITH releases_ranked AS (
  SELECT release_id, tenant_id, created_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY created_at DESC) AS rn
  FROM releases
),
releases_eligible AS (
  SELECT count(*) AS n FROM releases_ranked
  WHERE rn > 1000 OR created_at < now() - interval '180 days'
),
closed_roots AS (
  SELECT comment_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM comments
  WHERE parent_comment_id IS NULL AND status = 'CLOSED'
),
roots_to_delete AS (
  SELECT comment_id FROM closed_roots
  WHERE rn > 5000 OR updated_at < now() - interval '30 days'
),
comments_eligible AS (
  SELECT count(*) AS n
  FROM comments
  WHERE comment_id IN (SELECT comment_id FROM roots_to_delete)
     OR parent_comment_id IN (SELECT comment_id FROM roots_to_delete)
)
INSERT INTO retention_runs (policy_name, table_name, dry_run, eligible_count, deleted_count)
SELECT 'comments_releases', 'releases', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM releases_eligible
UNION ALL
SELECT 'comments_releases', 'comments', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM comments_eligible;

\if :dry_run
\else
BEGIN;

WITH releases_ranked AS (
  SELECT release_id, tenant_id, created_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY created_at DESC) AS rn
  FROM releases
),
releases_to_delete AS (
  SELECT release_id FROM releases_ranked
  WHERE rn > 1000 OR created_at < now() - interval '180 days'
)
DELETE FROM releases WHERE release_id IN (SELECT release_id FROM releases_to_delete);

WITH closed_roots AS (
  SELECT comment_id, tenant_id, updated_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY updated_at DESC) AS rn
  FROM comments
  WHERE parent_comment_id IS NULL AND status = 'CLOSED'
),
roots_to_delete AS (
  SELECT comment_id FROM closed_roots
  WHERE rn > 5000 OR updated_at < now() - interval '30 days'
)
DELETE FROM comments
WHERE comment_id IN (SELECT comment_id FROM roots_to_delete)
   OR parent_comment_id IN (SELECT comment_id FROM roots_to_delete);

COMMIT;
\endif
