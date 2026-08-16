-- The serial queue's own read: the tenant's oldest queued release, and the check
-- for one already in flight. Both walk (tenant_id, status) ordered by the
-- UUIDv7 release_id, which is the FIFO order.
CREATE INDEX releases_tenant_status_release_idx
  ON releases (tenant_id, status, release_id);

CREATE INDEX releases_tenant_review_idx
  ON releases (tenant_id, review_id);

-- At most one release in flight per tenant. This is the serialisation invariant
-- itself, not an optimisation: `erun release` bumps a semver, writes
-- version-bearing files, tags and pushes, so two concurrent releases on one
-- version line corrupt it. Enforcing it as a unique index means two claimers
-- racing lose one of the two in the database rather than both believing they
-- won.
CREATE UNIQUE INDEX releases_tenant_running_key
  ON releases (tenant_id)
  WHERE status = 'running';
