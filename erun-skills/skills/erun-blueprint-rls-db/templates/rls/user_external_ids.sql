ALTER TABLE user_external_ids ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_external_ids FORCE ROW LEVEL SECURITY;

CREATE POLICY user_external_ids_tenant_isolation
  ON user_external_ids
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY user_external_ids_operations_access
  ON user_external_ids
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
