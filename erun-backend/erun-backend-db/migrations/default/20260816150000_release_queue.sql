-- The per-tenant serial release queue. A review that reaches its merge commit
-- enqueues a release here; the executor claims the tenant's oldest queued row,
-- runs `erun release` as a Job, and records the version it minted plus the build
-- it wrote against the review.
--
-- One row per (tenant, commit) is the idempotency contract: re-triggering an
-- already-released commit cannot insert a second row, so it cannot mint a second
-- version for one merge commit. A failed release keeps its row and re-queues it
-- with a bumped `attempt`, which is what keys the Job and the durable workflow so
-- a retry runs instead of replaying the previous attempt's outcome.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/releases.sql, schema/indexes/releases.sql
-- and schema/rls/releases.sql.
CREATE TABLE "releases" (
  "release_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "review_id" uuid NULL,
  "target_branch" text NOT NULL,
  "commit_id" text NOT NULL,
  "status" text NOT NULL DEFAULT 'queued',
  "attempt" integer NOT NULL DEFAULT 1,
  "version" text NULL,
  "build_id" uuid NULL,
  "failure_reason" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("release_id"),
  CONSTRAINT "releases_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "releases_review_fkey" FOREIGN KEY ("tenant_id", "review_id") REFERENCES "reviews" ("tenant_id", "review_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "releases_build_fkey" FOREIGN KEY ("tenant_id", "build_id") REFERENCES "builds" ("tenant_id", "build_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "releases_target_branch_check" CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT "releases_commit_id_check" CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT "releases_status_check" CHECK (status IN ('queued', 'running', 'released', 'failed')),
  CONSTRAINT "releases_attempt_check" CHECK (attempt > 0),
  CONSTRAINT "releases_released_version_check" CHECK (
    status <> 'released' OR (version IS NOT NULL AND length(trim(version)) > 0)
  ),
  CONSTRAINT "releases_tenant_commit_key" UNIQUE ("tenant_id", "commit_id"),
  CONSTRAINT "releases_tenant_release_key" UNIQUE ("tenant_id", "release_id")
);

CREATE INDEX "releases_tenant_status_release_idx" ON "releases" ("tenant_id", "status", "release_id");
CREATE INDEX "releases_tenant_review_idx" ON "releases" ("tenant_id", "review_id");

CREATE TRIGGER releases_set_timestamps
  BEFORE INSERT OR UPDATE ON "releases"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "releases"
  TO erun_tenant, erun_operations;

ALTER TABLE "releases" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "releases" FORCE ROW LEVEL SECURITY;
CREATE POLICY releases_tenant_isolation
  ON "releases"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY releases_operations_access
  ON "releases"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
