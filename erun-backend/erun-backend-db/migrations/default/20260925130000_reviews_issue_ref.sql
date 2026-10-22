-- A review can now record the issue its work belongs to
-- (schema/tables/reviews.sql).
--
-- The link used to be resolved on every read from the source branch's name
-- under the documented branch convention, which means it was a guess about
-- the branch rather than a statement by its author, and it was marked
-- INFERRED for exactly that reason. A declared reference is the other half:
-- the author says which issue this is, and the branch name stops being the
-- only thing the platform can go on.
--
-- Nullable, not NOT NULL: most reviews predate the column and the column
-- cannot be backfilled honestly from a branch name -- writing an inferred
-- number in as though it had been declared is the one thing the provenance
-- distinction exists to prevent. NULL means "declared none", which is what
-- those rows are; the branch derivation still answers for them on read.
--
-- Spelled owner/repo#number, the same canonical key jobs.issue_ref holds, so a
-- view spanning both pipelines can join them.
ALTER TABLE "reviews" ADD COLUMN "issue_ref" text;

ALTER TABLE "reviews" ADD CONSTRAINT "reviews_issue_ref_check"
  CHECK ("issue_ref" IS NULL OR length(trim("issue_ref")) > 0);
