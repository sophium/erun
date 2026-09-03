-- Retention sweep for review comments and releases. See
-- erun-backend-db/AGENTS.md, "Review Comments And Releases" for the design
-- these bounds implement.
--
-- Required variable: dry_run (true|false). Run via:
--   psql -v ON_ERROR_STOP=1 -v dry_run=true|false -f comments_releases.sql "$ERUN_DATABASE_URL"

SET ROLE erun_operations;

-- releases: 180-day age bound or 1,000-most-recent-per-tenant count cap,
-- either bound prunes. Nothing references releases, so no guard is needed
-- beyond the bound itself.
WITH releases_ranked AS (
  SELECT release_id, tenant_id, created_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY created_at DESC) AS rn
  FROM releases
)
SELECT 'releases' AS table_name, count(*) AS eligible_for_deletion
FROM releases_ranked
WHERE rn > 1000 OR created_at < now() - interval '180 days';

-- comments: 30-day age bound (on updated_at, i.e. when a thread was closed)
-- or 5,000-most-recent-per-tenant count cap, applied only to CLOSED root
-- threads -- an OPEN thread is exempt regardless of age. A qualifying root
-- deletes together with every reply on it so a thread can never be
-- orphaned.
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
