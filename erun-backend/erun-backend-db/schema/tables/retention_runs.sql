-- Records the outcome of one retention policy file's sweep of one table,
-- across every tenant -- not tenant-owned itself, since a sweep runs once
-- for the whole platform rather than per tenant (same reasoning as
-- platform_rate_limits: this is platform-operational state, not tenant
-- data). This is what lets an operator see that a scheduled retention run
-- deleted rows and how many, without relying on the CronJob's own
-- transient pod logs (capped at successfulJobsHistoryLimit/
-- failedJobsHistoryLimit and only reachable with cluster access).
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
  -- A dry run never deletes, so deleted_count must be 0; a real run deletes
  -- exactly the rows it reported eligible under the same predicate, so
  -- deleted_count can never exceed eligible_count.
  CONSTRAINT retention_runs_deleted_count_check CHECK (
    deleted_count >= 0 AND deleted_count <= eligible_count
  ),
  CONSTRAINT retention_runs_dry_run_deleted_count_check CHECK (
    NOT dry_run OR deleted_count = 0
  )
);
