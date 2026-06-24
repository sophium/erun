ALTER TABLE environments ENABLE ROW LEVEL SECURITY;
ALTER TABLE environments FORCE ROW LEVEL SECURITY;

CREATE POLICY environments_tenant_isolation
  ON environments
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY environments_operations_access
  ON environments
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
