-- Retention sweep for reviews. See erun-backend-db/AGENTS.md, "Reviews" for
-- the design this implements, and its own header comment for why this file
-- is narrower than that design's full recommendation.
--
-- Scope narrowed deliberately from the design: only CLOSED reviews that
-- never reached MERGED are pruned. MERGED reviews are exempt entirely --
-- the design's own conservative default, since "branch history stays
-- unbounded" (this file's Multi-Tenant Database Plan) may be a deliberate
-- product promise that only the operator can waive. A review's status is a
-- single exclusive value (reviews_status_check), so status = 'CLOSED' alone
-- already excludes every review that ever reached MERGED -- no separate
-- last_merged_build_id check is needed.
--
-- Referential order matters here in a way it didn't for comments/releases:
-- reviews is the parent every review-lifecycle table hangs off, with six
-- inbound FKs, five of them RESTRICT. The design's own guard list names
-- comments/review_reviewers/releases/gate_runs but omits builds -- builds.
-- review_id is RESTRICT exactly like the other four, and any review that
-- ever had a GATE build attempt (the common case) has at least one builds
-- row pointing at it regardless of whether that build is one of the
-- pinned last_*_build_id columns. Deleting a review while an unpinned
-- builds/gate_runs row still references it would hit a live FK violation,
-- so this file's guard checks builds directly rather than trusting that
-- #1956's (not-yet-implemented) sweep already cleared it -- see the header
-- note on effectiveness below.
--
-- review_reviewers has no self-cleanup path anywhere in the codebase (see
-- the design), so this sweep prunes it itself, in the same transaction,
-- for exactly the reviews it is about to delete -- never for a review that
-- survives this round, so "who reviewed this" is never lost for a review
-- still standing.
--
-- Effectiveness note: until #1956 (builds/gate_runs retention) ships, a
-- CLOSED review that ever had any GATE build attempt keeps at least one
-- builds row forever, which fails this file's own builds guard forever.
-- That is expected, not a bug: this policy is still correct and safe to run
-- now, it will simply find few or no eligible rows on a tenant whose closed
-- reviews all went through at least one gate attempt, until #1956 lands and
-- starts aging those builds/gate_runs rows out.
--
-- Blast radius if the bound below is misconfigured: an unrecoverable loss
-- of review history and its reviewer assignments -- not a crash, since
-- nothing reads a deleted review to make a live decision. The builds/
-- comments/releases/gate_runs/review_merge_queue guards below bound that
-- loss to reviews with no surviving artifact of their own, but a bound
-- written too loose still discards real review provenance sooner than a
-- real audit would want.
--
-- Starting constants (not in the original design, which left reviews'
-- specific age/count numbers unspecified pending the product decision
-- above): a 90-day age bound on updated_at (the timestamp trigger stamps
-- it when status moves to CLOSED) -- between comments' 30 days and
-- releases' 180 days, reflecting that a closed review is a richer
-- investigative record than one comment thread but a weaker durable
-- identity than a release -- plus a 2,000-per-tenant count cap, the same
-- order of magnitude as builds' cap since review creation tracks or
-- exceeds merge rate. Revisit once real per-tenant counts exist, the same
-- "tune from real counts" caveat every policy in this sweep has had to
-- give its own starting constants.
--
-- Required variable: dry_run (true|false). Run via:
--   psql -v ON_ERROR_STOP=1 -v dry_run=true|false -f reviews.sql "$ERUN_DATABASE_URL"

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
    AND NOT EXISTS (SELECT 1 FROM gate_runs g WHERE g.tenant_id = e.tenant_id AND g.review_id = e.review_id)
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
    AND NOT EXISTS (SELECT 1 FROM gate_runs g WHERE g.tenant_id = e.tenant_id AND g.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
SELECT 'review_reviewers' AS table_name, count(*) AS eligible_for_deletion
FROM review_reviewers rr
WHERE (rr.tenant_id, rr.review_id) IN (SELECT tenant_id, review_id FROM guarded_eligible);

\if :dry_run
\else
BEGIN;

-- Null stale last_failed_build_id/last_ready_build_id pins on reviews past
-- the age/count bound, regardless of whether they pass the guard this
-- round. This is what eventually lets #1956's own sweep (once it exists)
-- prune the now-unpinned build -- see the header note above.
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
    AND NOT EXISTS (SELECT 1 FROM gate_runs g WHERE g.tenant_id = e.tenant_id AND g.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
-- review_reviewers deletes before reviews, in the same transaction, so a
-- reviewer row is only ever removed together with the review it assigns.
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
    AND NOT EXISTS (SELECT 1 FROM gate_runs g WHERE g.tenant_id = e.tenant_id AND g.review_id = e.review_id)
    AND NOT EXISTS (SELECT 1 FROM review_merge_queue q WHERE q.tenant_id = e.tenant_id AND q.review_id = e.review_id)
)
DELETE FROM reviews r
WHERE (r.tenant_id, r.review_id) IN (SELECT tenant_id, review_id FROM guarded_eligible);

COMMIT;
\endif
