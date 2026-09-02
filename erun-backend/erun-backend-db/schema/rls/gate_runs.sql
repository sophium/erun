ALTER TABLE gate_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE gate_runs FORCE ROW LEVEL SECURITY;

CREATE POLICY gate_runs_tenant_isolation
  ON gate_runs
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY gate_runs_operations_access
  ON gate_runs
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
