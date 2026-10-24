ALTER TABLE invites ENABLE ROW LEVEL SECURITY;
ALTER TABLE invites FORCE ROW LEVEL SECURITY;

CREATE POLICY invites_tenant_isolation
  ON invites
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

-- Also backs the unauthenticated invite-accept token lookup, which has no tenant yet
-- (see repository.TxManager.WithinSystemTx).
CREATE POLICY invites_operations_access
  ON invites
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
