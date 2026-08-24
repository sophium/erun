ALTER TABLE review_reviewers ENABLE ROW LEVEL SECURITY;
ALTER TABLE review_reviewers FORCE ROW LEVEL SECURITY;

CREATE POLICY review_reviewers_tenant_isolation
  ON review_reviewers
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY review_reviewers_operations_access
  ON review_reviewers
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
