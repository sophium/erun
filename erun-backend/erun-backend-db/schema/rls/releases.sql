ALTER TABLE releases ENABLE ROW LEVEL SECURITY;
ALTER TABLE releases FORCE ROW LEVEL SECURITY;

CREATE POLICY releases_tenant_isolation
  ON releases
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY releases_operations_access
  ON releases
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
