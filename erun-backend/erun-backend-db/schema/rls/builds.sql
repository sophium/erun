ALTER TABLE builds ENABLE ROW LEVEL SECURITY;
ALTER TABLE builds FORCE ROW LEVEL SECURITY;

CREATE POLICY builds_tenant_isolation
  ON builds
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY builds_operations_access
  ON builds
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
