ALTER TABLE usage_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_events FORCE ROW LEVEL SECURITY;

CREATE POLICY usage_events_tenant_isolation
  ON usage_events
  FOR SELECT
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id());

CREATE POLICY usage_events_tenant_insert
  ON usage_events
  FOR INSERT
  TO erun_tenant
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY usage_events_operations_select
  ON usage_events
  FOR SELECT
  TO erun_operations
  USING (true);

CREATE POLICY usage_events_operations_insert
  ON usage_events
  FOR INSERT
  TO erun_operations
  WITH CHECK (true);
