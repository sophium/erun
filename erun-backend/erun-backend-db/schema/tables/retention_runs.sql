-- Platform-wide record of one retention policy's sweep of one table, so deletions outlive the CronJob's pod logs.
-- Not audit_events, which needs an authenticated single-tenant caller. No retention policy sweeps this table.
CREATE TABLE retention_runs (
  retention_run_id UUID PRIMARY KEY DEFAULT uuidv7(),
  policy_name TEXT NOT NULL,
  table_name TEXT NOT NULL,
  dry_run BOOLEAN NOT NULL,
  eligible_count BIGINT NOT NULL,
  deleted_count BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT retention_runs_policy_name_check CHECK (length(trim(policy_name)) > 0),
  CONSTRAINT retention_runs_table_name_check CHECK (length(trim(table_name)) > 0),
  CONSTRAINT retention_runs_eligible_count_check CHECK (eligible_count >= 0),
  -- A dry run deletes nothing; a real run deletes at most what it reported eligible.
  CONSTRAINT retention_runs_deleted_count_check CHECK (
    deleted_count >= 0 AND deleted_count <= eligible_count
  ),
  CONSTRAINT retention_runs_dry_run_deleted_count_check CHECK (
    NOT dry_run OR deleted_count = 0
  )
);
