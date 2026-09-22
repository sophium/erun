-- A review now records which repository it belongs to (schema/tables/reviews.sql).
-- Before this, a row carried only source_branch/target_branch, so a tenant
-- serving more than one repository had no way to say which repository a
-- review's branches named: two repositories sharing a target branch collided
-- in one merge queue, and the queue a caller saw was whichever tenant its
-- credential reached rather than the repository's own.
--
-- Nullable, not NOT NULL: every existing review predates the column and the
-- remote it would hold was consumed at report-merged and never stored, so
-- there is genuinely nothing to backfill from. NULL means "not recorded",
-- which is what those rows are; the platform records the repository the first
-- time a repository-bearing call names one for them (report-merged's own
-- remote). New reviews created through erun's clients always carry one.
ALTER TABLE "reviews" ADD COLUMN "repository" text;

ALTER TABLE "reviews" ADD CONSTRAINT "reviews_repository_check"
  CHECK (repository IS NULL OR length(trim(repository)) > 0);

-- Name uniqueness, re-scoped (schema/indexes/reviews.sql). The old
-- table-level UNIQUE (tenant_id, name) becomes a partial unique index for two
-- reasons, both of them the same defect seen from different sides: a name is
-- the squash-merge message, so it is only claimed within the repository that
-- would carry it, and only by a review that can still reach MERGED or did
-- reach it. A CLOSED review never landed and reserves nothing, so re-opening
-- work on a rebased branch can reuse the name it was created with.
--
-- NULLS NOT DISTINCT keeps reviews created before this column grouping by
-- name exactly as they do today, instead of every NULL-repository row being
-- unique by virtue of its NULL.
ALTER TABLE "reviews" DROP CONSTRAINT "reviews_tenant_name_key";

CREATE UNIQUE INDEX "reviews_tenant_repository_name_idx"
  ON "reviews" ("tenant_id", "repository", "name") NULLS NOT DISTINCT
  WHERE status <> 'CLOSED';

-- One live review per branch pair, now per repository
-- (schema/indexes/reviews.sql). The same source/target pair in two
-- repositories a tenant serves are two different proposals, not a conflict;
-- within one repository the invariant is unchanged.
DROP INDEX "reviews_tenant_live_source_target_idx";

CREATE UNIQUE INDEX "reviews_tenant_live_source_target_idx"
  ON "reviews" ("tenant_id", "repository", "source_branch", "target_branch") NULLS NOT DISTINCT
  WHERE (status NOT IN ('MERGED', 'CLOSED'));

-- The merge queue resolves a repository's own waiting reviews by this pair.
CREATE INDEX "reviews_tenant_repository_idx"
  ON "reviews" ("tenant_id", "repository");
