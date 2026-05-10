ALTER TABLE comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE comments FORCE ROW LEVEL SECURITY;

CREATE POLICY comments_tenant_isolation
  ON comments
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY comments_operations_access
  ON comments
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
