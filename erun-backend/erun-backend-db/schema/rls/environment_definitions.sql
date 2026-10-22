ALTER TABLE environment_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE environment_definitions FORCE ROW LEVEL SECURITY;

CREATE POLICY environment_definitions_tenant_isolation
  ON environment_definitions
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY environment_definitions_operations_access
  ON environment_definitions
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
