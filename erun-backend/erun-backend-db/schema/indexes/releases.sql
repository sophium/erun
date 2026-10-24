-- Serial queue reads: oldest queued and in-flight release, in UUIDv7 (FIFO) order.
CREATE INDEX releases_tenant_status_release_idx
  ON releases (tenant_id, status, release_id);

CREATE INDEX releases_tenant_review_idx
  ON releases (tenant_id, review_id);

-- At most one release in flight per tenant: concurrent releases corrupt the version line,
-- and the unique index makes one of two racing claimers lose.
CREATE UNIQUE INDEX releases_tenant_running_key
  ON releases (tenant_id)
  WHERE status = 'running';
