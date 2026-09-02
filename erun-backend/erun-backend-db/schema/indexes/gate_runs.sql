CREATE INDEX gate_runs_tenant_target_created_at_idx
  ON gate_runs (tenant_id, target_branch, created_at DESC);

CREATE INDEX gate_runs_tenant_status_idx
  ON gate_runs (tenant_id, status);

CREATE INDEX gate_runs_tenant_review_idx
  ON gate_runs (tenant_id, review_id)
  WHERE review_id IS NOT NULL;
