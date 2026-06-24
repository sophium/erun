ALTER TABLE tenant_quotas ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_quotas FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_quotas_tenant_isolation
  ON tenant_quotas
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY tenant_quotas_operations_access
  ON tenant_quotas
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
