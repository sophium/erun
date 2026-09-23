-- A build's environment reference now carries the tenant, the way jobs,
-- ai_sessions and environment_events already do.
--
-- builds.environment_id was a single-column foreign key to
-- environments.environment_id while every sibling table referenced
-- environments by (tenant_id, environment_id). The tenant was therefore not
-- part of the reference at all: a build row could name an environment
-- belonging to another tenant, and the schema accepted it. Combined with a
-- route that took a caller-supplied environment id without checking it --
-- and with a row whose own tenant_id is the caller's, so row-level security
-- passed -- the difference between a created build and a refusal reported
-- whether an environment id existed in *any* tenant. That is an existence
-- oracle over every tenant's ids, reachable by any tenant user rather than
-- only by an operations-scoped caller.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/builds.sql.

-- Pre-existing rows. A NULL environment_id -- a review-linked build, per the
-- column's own contract -- stays valid unchanged: the default MATCH SIMPLE
-- semantics skip the check whenever any referencing column is NULL, so no
-- backfill is needed for those rows, and there would be no tenant to guess
-- for one anyway.
--
-- A row whose environment_id is set but does not name one of its own
-- tenant's environments cannot satisfy the new reference. There is nothing
-- truthful to backfill it to: the row's tenant_id is the only tenant the
-- platform can justify, the referenced environment does not belong to it,
-- and resolving the id under some other tenant -- or inventing one -- would
-- fabricate a fact about which environment that build ran in. So the
-- reference is cleared instead, which is exactly the shape an environment
-- deletion already leaves behind (see the ON DELETE below): the build row,
-- its outcome, and its commit survive, and only the environment claim is
-- dropped. That claim was one this tenant was never entitled to read.
UPDATE "builds"
  SET "environment_id" = NULL
  WHERE "environment_id" IS NOT NULL
    AND NOT EXISTS (
      SELECT 1
      FROM "environments" e
      WHERE e."tenant_id" = "builds"."tenant_id"
        AND e."environment_id" = "builds"."environment_id"
    );

ALTER TABLE "builds" DROP CONSTRAINT "builds_environment_id_fkey";

-- ON DELETE SET NULL names environment_id, not the whole key: deleting an
-- environment must still clear only the build's reference to it, exactly as
-- it did before this change. A bare SET NULL over a composite key would try
-- to null tenant_id as well, which is NOT NULL, and the delete would fail.
ALTER TABLE "builds"
  ADD CONSTRAINT "builds_tenant_environment_fkey"
  FOREIGN KEY ("tenant_id", "environment_id") REFERENCES "environments" ("tenant_id", "environment_id")
  ON UPDATE NO ACTION ON DELETE SET NULL ("environment_id");
