ALTER TABLE context_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE context_credentials FORCE ROW LEVEL SECURITY;

CREATE POLICY context_credentials_tenant_isolation
  ON context_credentials
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY context_credentials_operations_access
  ON context_credentials
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
