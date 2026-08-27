ALTER TABLE invites ENABLE ROW LEVEL SECURITY;
ALTER TABLE invites FORCE ROW LEVEL SECURITY;

CREATE POLICY invites_tenant_isolation
  ON invites
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

-- erun_operations also backs the unauthenticated accept endpoint's
-- token lookup: nobody has signed in yet when a token is presented, so
-- there is no tenant to scope the initial SELECT by. See
-- repository.TxManager.WithinSystemTx for the narrow, reviewable place
-- that role is assumed outside a real operations-tenant caller.
CREATE POLICY invites_operations_access
  ON invites
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
