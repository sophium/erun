-- A gate run is the first-class record of one attempt to gate a prospective
-- merge -- the branch, the merge commit actually tested, the target, and the
-- verdict -- independent of whether an erun review exists for the change.
-- Reviews remain one producer of gate runs (a review-driven merge reports
-- one against its GATE build's commit); a repository whose changes arrive as
-- GitHub pull requests, with no erun review at all, is the other.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/gate_runs.sql, schema/indexes/gate_runs.sql,
-- schema/rls/gate_runs.sql, and schema/roles.sql.

CREATE TABLE "gate_runs" (
  "gate_run_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "source_branch" text NOT NULL,
  "target_branch" text NOT NULL,
  "source_commit" text NOT NULL,
  "merge_commit" text NULL,
  "review_id" uuid NULL,
  "status" text NOT NULL DEFAULT 'RUNNING',
  "failing_step" text NULL,
  "log_ref" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("gate_run_id"),
  CONSTRAINT "gate_runs_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "gate_runs_tenant_review_fkey" FOREIGN KEY ("tenant_id", "review_id") REFERENCES "reviews" ("tenant_id", "review_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "gate_runs_status_check" CHECK ("status" IN ('RUNNING', 'PASSED', 'FAILED', 'INCONCLUSIVE')),
  CONSTRAINT "gate_runs_source_branch_check" CHECK (length(trim("source_branch")) > 0),
  CONSTRAINT "gate_runs_target_branch_check" CHECK (length(trim("target_branch")) > 0),
  CONSTRAINT "gate_runs_source_commit_check" CHECK (length(trim("source_commit")) > 0),
  CONSTRAINT "gate_runs_merge_commit_check" CHECK ("merge_commit" IS NULL OR length(trim("merge_commit")) > 0),
  CONSTRAINT "gate_runs_merge_commit_required_check" CHECK ("status" IN ('FAILED', 'INCONCLUSIVE') OR "merge_commit" IS NOT NULL),
  CONSTRAINT "gate_runs_failing_step_check" CHECK ("status" <> 'FAILED' OR ("failing_step" IS NOT NULL AND length(trim("failing_step")) > 0)),
  CONSTRAINT "gate_runs_tenant_gate_run_key" UNIQUE ("tenant_id", "gate_run_id")
);

CREATE INDEX "gate_runs_tenant_target_created_at_idx" ON "gate_runs" ("tenant_id", "target_branch", "created_at" DESC);
CREATE INDEX "gate_runs_tenant_status_idx" ON "gate_runs" ("tenant_id", "status");
CREATE INDEX "gate_runs_tenant_review_idx" ON "gate_runs" ("tenant_id", "review_id") WHERE ("review_id" IS NOT NULL);

CREATE TRIGGER "gate_runs_set_timestamps"
  BEFORE INSERT OR UPDATE ON "gate_runs"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "gate_runs"
  TO erun_tenant, erun_operations;

ALTER TABLE "gate_runs" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "gate_runs" FORCE ROW LEVEL SECURITY;

CREATE POLICY gate_runs_tenant_isolation
  ON "gate_runs"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY gate_runs_operations_access
  ON "gate_runs"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
