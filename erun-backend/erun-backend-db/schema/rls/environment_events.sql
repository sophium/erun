ALTER TABLE environment_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE environment_events FORCE ROW LEVEL SECURITY;

CREATE POLICY environment_events_tenant_isolation
  ON environment_events
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY environment_events_operations_access
  ON environment_events
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
