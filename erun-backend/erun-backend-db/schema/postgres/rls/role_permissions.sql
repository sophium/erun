ALTER TABLE role_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE role_permissions FORCE ROW LEVEL SECURITY;

CREATE POLICY role_permissions_tenant_isolation
  ON role_permissions
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY role_permissions_operations_access
  ON role_permissions
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
