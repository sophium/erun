-- A build no longer has to belong to a review. Every ordinary `erun build`
-- run in an environment (agents run this continuously) previously had
-- nowhere to be recorded, since review_id was a required column -- the
-- Builds tab could only ever show a review's own RECORDED/GATE builds.
-- review_id becomes optional and environment_id (nullable, SET NULL on
-- environment delete, mirroring usage_events.environment_id) becomes the
-- identity an unattached build reports instead: tenant + environment +
-- commit + version, not a review.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/builds.sql and
-- schema/indexes/builds.sql.

ALTER TABLE "builds" ALTER COLUMN "review_id" DROP NOT NULL;

ALTER TABLE "builds" ADD COLUMN "environment_id" uuid NULL;

ALTER TABLE "builds"
  ADD CONSTRAINT "builds_environment_id_fkey"
  FOREIGN KEY ("environment_id") REFERENCES "environments" ("environment_id")
  ON UPDATE NO ACTION ON DELETE SET NULL;

-- A GATE build always belongs to the review it gates; only the new
-- review-less path (RECORDED, reported by an ordinary `erun build`) may omit
-- review_id.
ALTER TABLE "builds"
  ADD CONSTRAINT "builds_gate_requires_review_check"
  CHECK ("kind" <> 'GATE' OR "review_id" IS NOT NULL);

CREATE INDEX "builds_tenant_created_at_idx" ON "builds" ("tenant_id", "created_at" DESC);

CREATE INDEX "builds_tenant_environment_created_at_idx" ON "builds" ("tenant_id", "environment_id", "created_at" DESC)
  WHERE ("environment_id" IS NOT NULL);
