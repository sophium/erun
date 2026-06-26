ALTER TABLE cloud_provider_aliases ENABLE ROW LEVEL SECURITY;
ALTER TABLE cloud_provider_aliases FORCE ROW LEVEL SECURITY;

CREATE POLICY cloud_provider_aliases_tenant_isolation
  ON cloud_provider_aliases
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY cloud_provider_aliases_operations_access
  ON cloud_provider_aliases
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
