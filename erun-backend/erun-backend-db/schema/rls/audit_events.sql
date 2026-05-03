ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_events_tenant_isolation
  ON audit_events
  FOR SELECT
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id());

CREATE POLICY audit_events_tenant_insert
  ON audit_events
  FOR INSERT
  TO erun_tenant
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY audit_events_operations_select
  ON audit_events
  FOR SELECT
  TO erun_operations
  USING (true);

CREATE POLICY audit_events_operations_insert
  ON audit_events
  FOR INSERT
  TO erun_operations
  WITH CHECK (true);
