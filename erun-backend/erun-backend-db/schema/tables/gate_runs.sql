CREATE TABLE gate_runs (
  gate_run_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  source_branch TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  source_commit TEXT NOT NULL,
  -- merge_commit is the prospective squash-merge commit this run actually
  -- tested. NULL only for a run that failed before that commit existed at
  -- all (e.g. a squash conflict) — see gate_runs_merge_commit_check.
  merge_commit TEXT,
  -- review_id links this run to the erun review it gates, when one exists.
  -- NULL is the common case for a repository whose changes arrive as GitHub
  -- pull requests rather than erun reviews: the gate still runs and still
  -- needs to be seen, with nothing else to attach it to.
  review_id UUID,
  status TEXT NOT NULL DEFAULT 'RUNNING',
  -- failing_step names which gate step produced a FAILED verdict (e.g. "erun
  -- build", "git merge --squash"); required exactly when status is FAILED, so
  -- a red verdict can never be reported with nothing to point at.
  failing_step TEXT,
  -- log_ref points at where to read the run's own output (a job id, URL, or
  -- path); optional even for a FAILED run, since not every caller has one.
  log_ref TEXT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT gate_runs_tenant_review_fkey FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  CONSTRAINT gate_runs_status_check CHECK (status IN ('RUNNING', 'PASSED', 'FAILED', 'INCONCLUSIVE')),
  CONSTRAINT gate_runs_source_branch_check CHECK (length(trim(source_branch)) > 0),
  CONSTRAINT gate_runs_target_branch_check CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT gate_runs_source_commit_check CHECK (length(trim(source_commit)) > 0),
  CONSTRAINT gate_runs_merge_commit_check CHECK (merge_commit IS NULL OR length(trim(merge_commit)) > 0),
  CONSTRAINT gate_runs_merge_commit_required_check CHECK (status IN ('FAILED', 'INCONCLUSIVE') OR merge_commit IS NOT NULL),
  -- A FAILED verdict must always name which step failed, so "red" is never
  -- reported with nothing for a reader to act on. RUNNING/PASSED/INCONCLUSIVE
  -- carry no such requirement: INCONCLUSIVE is deliberately unable to name a
  -- failing step, since the whole point is that the gate itself never reached
  -- a real verdict.
  CONSTRAINT gate_runs_failing_step_check CHECK (status <> 'FAILED' OR (failing_step IS NOT NULL AND length(trim(failing_step)) > 0)),
  CONSTRAINT gate_runs_tenant_gate_run_key UNIQUE (tenant_id, gate_run_id)
);
