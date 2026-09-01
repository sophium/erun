ALTER TABLE ai_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY ai_sessions_tenant_isolation
  ON ai_sessions
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY ai_sessions_operations_access
  ON ai_sessions
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
