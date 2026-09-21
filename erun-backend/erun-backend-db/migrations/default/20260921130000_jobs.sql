-- Records work an agent or orchestrator is doing *now*, as first-class
-- platform state -- the record of intent the platform otherwise has none of.
-- Builds and gate runs record outcomes after the fact; a job is claimed
-- before the work starts, so a second actor asking for the same scope is
-- told who already holds it and what they are doing.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/jobs.sql, schema/indexes/jobs.sql,
-- schema/rls/jobs.sql, schema/triggers/timestamps.sql, and schema/roles.sql.

CREATE TABLE "jobs" (
  "job_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "environment_id" uuid NULL,
  "job_type" text NOT NULL,
  "issue_ref" text NULL,
  "summary" text NOT NULL,
  "status" text NOT NULL DEFAULT 'RUNNING',
  "actor_kind" text NOT NULL,
  "actor_id" text NOT NULL,
  "scope" text NULL,
  "local_job_id" text NULL,
  "started_at" timestamptz NOT NULL DEFAULT now(),
  "ended_at" timestamptz NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("job_id"),
  CONSTRAINT "jobs_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "jobs_tenant_environment_fkey" FOREIGN KEY ("tenant_id", "environment_id") REFERENCES "environments" ("tenant_id", "environment_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "jobs_job_type_check" CHECK ("job_type" IN ('fix', 'review', 'gate', 'release', 'deploy', 'investigate', 'plan', 'triage', 'maintenance')),
  CONSTRAINT "jobs_status_check" CHECK ("status" IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'ABANDONED', 'SUPERSEDED')),
  CONSTRAINT "jobs_actor_kind_check" CHECK ("actor_kind" IN ('agent', 'orchestrator', 'human')),
  CONSTRAINT "jobs_summary_check" CHECK (length(trim("summary")) > 0),
  CONSTRAINT "jobs_actor_id_check" CHECK (length(trim("actor_id")) > 0),
  CONSTRAINT "jobs_issue_ref_check" CHECK ("issue_ref" IS NULL OR length(trim("issue_ref")) > 0),
  CONSTRAINT "jobs_scope_check" CHECK ("scope" IS NULL OR length(trim("scope")) > 0),
  CONSTRAINT "jobs_local_job_id_check" CHECK ("local_job_id" IS NULL OR length(trim("local_job_id")) > 0),
  CONSTRAINT "jobs_ended_at_check" CHECK (("status" = 'RUNNING') = ("ended_at" IS NULL)),
  CONSTRAINT "jobs_tenant_job_key" UNIQUE ("tenant_id", "job_id")
);

CREATE INDEX "jobs_tenant_status_idx" ON "jobs" ("tenant_id", "status");
CREATE INDEX "jobs_tenant_issue_ref_idx" ON "jobs" ("tenant_id", "issue_ref") WHERE ("issue_ref" IS NOT NULL);
CREATE INDEX "jobs_tenant_scope_status_idx" ON "jobs" ("tenant_id", "scope", "status");
CREATE INDEX "jobs_tenant_started_at_idx" ON "jobs" ("tenant_id", "started_at" DESC);

CREATE TRIGGER "jobs_set_timestamps"
  BEFORE INSERT OR UPDATE ON "jobs"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "jobs"
  TO erun_tenant, erun_operations;

ALTER TABLE "jobs" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "jobs" FORCE ROW LEVEL SECURITY;

CREATE POLICY jobs_tenant_isolation
  ON "jobs"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY jobs_operations_access
  ON "jobs"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
