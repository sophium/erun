CREATE INDEX jobs_tenant_status_idx
  ON jobs (tenant_id, status);

CREATE INDEX jobs_tenant_issue_ref_idx
  ON jobs (tenant_id, issue_ref)
  WHERE issue_ref IS NOT NULL;

-- The claim query: is an open job already holding this scope in this tenant?
CREATE INDEX jobs_tenant_scope_status_idx
  ON jobs (tenant_id, scope, status);

CREATE INDEX jobs_tenant_started_at_idx
  ON jobs (tenant_id, started_at DESC);
