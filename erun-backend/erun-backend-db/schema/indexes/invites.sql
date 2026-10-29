-- The console lists only outstanding invites.
CREATE INDEX invites_tenant_outstanding_idx
  ON invites (tenant_id, created_at)
  WHERE consumed_at IS NULL;
