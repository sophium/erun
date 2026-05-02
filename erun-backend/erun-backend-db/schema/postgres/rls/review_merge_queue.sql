ALTER TABLE review_merge_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE review_merge_queue FORCE ROW LEVEL SECURITY;

CREATE POLICY review_merge_queue_tenant_isolation
  ON review_merge_queue
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY review_merge_queue_operations_access
  ON review_merge_queue
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
