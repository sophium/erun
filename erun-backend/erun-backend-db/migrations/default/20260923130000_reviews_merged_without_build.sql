-- A MERGED review no longer has to name a merge build (schema/tables/reviews.sql).
--
-- reviews_status_build_link_check has always required every MERGED row to carry
-- a last_merged_build_id, and that held while the merge queue was the only way
-- a review could reach MERGED: the environment the queue promoted recorded the
-- GATE build first, so there was always one to name.
--
-- report-merged reconciles the other kind of landing — a branch squash-merged on
-- GitHub, or landed by merge commit or fast-forward, where no GATE build exists
-- to name because the queue never ran. Reconciliation deliberately records no
-- last_merged_build_id (it has none, and FindLastMergedReview's
-- "last_merged_build_id IS NOT NULL" filter is what keeps gatedTargetTip
-- anchored on a real build), so every such report violated this constraint and
-- surfaced to the caller as a bare 500 INTERNAL_SERVER_ERROR. The capability was
-- shipped, documented, unit-tested against a fake repository, and impossible
-- against a real database — no reconciling report had ever succeeded, for a
-- legacy row or any other.
--
-- Only the MERGED leg goes. FAILED, READY and MERGE still each have to name the
-- build that produced them, which is the invariant the merge queue's own
-- transitions rely on. For MERGED there is nothing left to condition on: the
-- platform's service code already refuses a MERGED report from a review holding
-- MERGE without a successful GATE build recorded against it
-- (ReviewService.acceptMerged), and that is where the check belongs — it is a
-- fact about a build row, which a CHECK constraint on this table cannot read.
ALTER TABLE "reviews" DROP CONSTRAINT "reviews_status_build_link_check";

ALTER TABLE "reviews" ADD CONSTRAINT "reviews_status_build_link_check" CHECK (
  (status <> 'FAILED' OR last_failed_build_id IS NOT NULL)
  AND (status <> 'READY' OR last_ready_build_id IS NOT NULL)
  AND (status <> 'MERGE' OR last_ready_build_id IS NOT NULL)
);
