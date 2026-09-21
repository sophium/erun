CREATE TABLE jobs (
  job_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  -- environment_id is the pod-side environment the job runs in. NULL is a
  -- real case, not a gap: host-side orchestrator work that never enters an
  -- environment still needs to be claimed and seen.
  environment_id UUID,
  -- job_type is the operator-facing kind of work, a closed vocabulary so a
  -- dashboard can group jobs without a free-text bucket that never converges.
  job_type TEXT NOT NULL,
  -- issue_ref is the issue this work belongs to ("sophium/erun#2109"). NULL
  -- when the work is not issue-driven.
  issue_ref TEXT,
  -- summary is prose describing what is being done -- never a command line.
  -- A row reading "git rebase --onto ..." describes nothing a reader can act
  -- on; EnvironmentJob.Command stays in the pod as the technical detail.
  summary TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'RUNNING',
  -- actor_kind and actor_id name who holds this job. actor_id is the thing
  -- an anonymous activity lease cannot supply: the refusal of a second claim
  -- on a held scope names it, so a refused caller knows who to ask.
  actor_kind TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  -- scope is what the job claims, for the dedup query (a branch, a component,
  -- or an issue ref). NULL means the job claims nothing and never collides.
  scope TEXT,
  -- local_job_id mirrors the in-pod EnvironmentJob.ID when there is one, so
  -- the platform row and the pod's own job record can be tied together.
  local_job_id TEXT,
  -- started_at is required: a claim's whole point is that a refused caller is
  -- told when the holder started, so a holder with no start time would make
  -- the refusal unactionable.
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  -- ended_at is NULL exactly while status is RUNNING, enforced below: a job
  -- that has stopped is never left without a time it stopped at.
  ended_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT jobs_tenant_environment_fkey FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id),
  CONSTRAINT jobs_job_type_check CHECK (job_type IN ('fix', 'review', 'gate', 'release', 'deploy', 'investigate', 'plan', 'triage', 'maintenance')),
  CONSTRAINT jobs_status_check CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'ABANDONED', 'SUPERSEDED')),
  CONSTRAINT jobs_actor_kind_check CHECK (actor_kind IN ('agent', 'orchestrator', 'human')),
  CONSTRAINT jobs_summary_check CHECK (length(trim(summary)) > 0),
  CONSTRAINT jobs_actor_id_check CHECK (length(trim(actor_id)) > 0),
  CONSTRAINT jobs_issue_ref_check CHECK (issue_ref IS NULL OR length(trim(issue_ref)) > 0),
  CONSTRAINT jobs_scope_check CHECK (scope IS NULL OR length(trim(scope)) > 0),
  CONSTRAINT jobs_local_job_id_check CHECK (local_job_id IS NULL OR length(trim(local_job_id)) > 0),
  CONSTRAINT jobs_ended_at_check CHECK ((status = 'RUNNING') = (ended_at IS NULL)),
  CONSTRAINT jobs_tenant_job_key UNIQUE (tenant_id, job_id)
);
