-- Outstanding (unconsumed) invites are what the console lists, so the
-- partial index only covers rows that are still usable rather than every
-- invite a tenant has ever issued.
CREATE INDEX invites_tenant_outstanding_idx
  ON invites (tenant_id, created_at)
  WHERE consumed_at IS NULL;
