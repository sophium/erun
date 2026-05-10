CREATE INDEX builds_tenant_review_created_at_idx
  ON builds (tenant_id, review_id, created_at);

CREATE INDEX builds_tenant_commit_idx
  ON builds (tenant_id, commit_id);
