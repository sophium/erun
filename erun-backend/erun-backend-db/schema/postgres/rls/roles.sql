ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE roles FORCE ROW LEVEL SECURITY;

CREATE POLICY roles_tenant_isolation
  ON roles
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY roles_operations_access
  ON roles
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
