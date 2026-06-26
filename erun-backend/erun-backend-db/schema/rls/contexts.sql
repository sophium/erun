ALTER TABLE contexts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contexts FORCE ROW LEVEL SECURITY;

CREATE POLICY contexts_tenant_isolation
  ON contexts
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY contexts_operations_access
  ON contexts
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
