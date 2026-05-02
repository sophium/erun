ALTER TABLE user_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_roles FORCE ROW LEVEL SECURITY;

CREATE POLICY user_roles_tenant_isolation
  ON user_roles
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY user_roles_operations_access
  ON user_roles
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
