-- builds_version_check has always required a non-empty version for a
-- RECORDED build, but only ever made a GATE build's version optional -- it
-- never actually forbade one, even though the comment right above the
-- column (schema/tables/builds.sql), erun-backend-db/AGENTS.md, and
-- erun-backend-api/AGENTS.md's Merge Queue section all state as a hard
-- invariant that a GATE build "publishes nothing and so mints no version".
-- The repository's Create column list passes a caller-supplied Version
-- straight through regardless of kind, so nothing enforced the documented
-- invariant anywhere. This tightens the CHECK to match what was always
-- claimed: version is required for RECORDED and forbidden for GATE.
ALTER TABLE "builds" DROP CONSTRAINT "builds_version_check";
ALTER TABLE "builds" ADD CONSTRAINT "builds_version_check"
  CHECK (
    (kind = 'RECORDED' AND version IS NOT NULL AND length(trim(version)) > 0)
    OR (kind = 'GATE' AND version IS NULL)
  );
