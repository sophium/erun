-- The serial queue's own read: the tenant's oldest queued release, and the check
-- for one already in flight. Both walk (tenant_id, status) ordered by the
-- UUIDv7 release_id, which is the FIFO order.
CREATE INDEX releases_tenant_status_release_idx
  ON releases (tenant_id, status, release_id);

CREATE INDEX releases_tenant_review_idx
  ON releases (tenant_id, review_id);
