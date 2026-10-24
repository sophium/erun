CREATE TABLE jobs (
  job_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  -- NULL for host-side orchestrator work that never enters an environment.
  environment_id UUID,
  -- Closed vocabulary so dashboards can group jobs.
  job_type TEXT NOT NULL,
  -- NULL when the work is not issue-driven.
  issue_ref TEXT,
  -- Prose describing the work, never a command line.
  summary TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'RUNNING',
  -- actor_id is named in a refused claim so the caller knows who to ask.
  actor_kind TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  -- The dedup claim key; NULL claims nothing and never collides.
  scope TEXT,
  -- The in-pod EnvironmentJob.ID, when there is one.
  local_job_id TEXT,
  -- Required: a refused claim reports when the holder started.
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT jobs_tenant_environment_fkey FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id),
  CONSTRAINT jobs_job_type_check CHECK (job_type IN ('fix', 'review', 'gate', 'release', 'deploy', 'investigate', 'plan', 'triage', 'maintenance')),
  CONSTRAINT jobs_status_check CHECK (status IN ('PLANNED', 'RUNNING', 'SUCCEEDED', 'FAILED', 'ABANDONED', 'SUPERSEDED')),
  CONSTRAINT jobs_actor_kind_check CHECK (actor_kind IN ('agent', 'orchestrator', 'human')),
  CONSTRAINT jobs_summary_check CHECK (length(trim(summary)) > 0),
  CONSTRAINT jobs_actor_id_check CHECK (length(trim(actor_id)) > 0),
  CONSTRAINT jobs_issue_ref_check CHECK (issue_ref IS NULL OR length(trim(issue_ref)) > 0),
  CONSTRAINT jobs_scope_check CHECK (scope IS NULL OR length(trim(scope)) > 0),
  CONSTRAINT jobs_local_job_id_check CHECK (local_job_id IS NULL OR length(trim(local_job_id)) > 0),
  CONSTRAINT jobs_ended_at_check CHECK ((status IN ('PLANNED', 'RUNNING')) = (ended_at IS NULL)),
  CONSTRAINT jobs_tenant_job_key UNIQUE (tenant_id, job_id)
);
