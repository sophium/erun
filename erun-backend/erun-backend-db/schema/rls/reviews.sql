ALTER TABLE reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE reviews FORCE ROW LEVEL SECURITY;

CREATE POLICY reviews_tenant_isolation
  ON reviews
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY reviews_operations_access
  ON reviews
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
