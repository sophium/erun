CREATE INDEX builds_tenant_review_created_at_idx
  ON builds (tenant_id, review_id, created_at);

CREATE INDEX builds_tenant_commit_idx
  ON builds (tenant_id, commit_id);

-- Serves GET /v1/builds' unfiltered and environment-filtered pages -- the
-- tenant-wide, paginated list an ordinary `erun build` report feeds, unlike
-- the review-scoped index above.
CREATE INDEX builds_tenant_created_at_idx
  ON builds (tenant_id, created_at DESC);

CREATE INDEX builds_tenant_environment_created_at_idx
  ON builds (tenant_id, environment_id, created_at DESC)
  WHERE environment_id IS NOT NULL;
