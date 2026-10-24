CREATE TABLE releases (
  release_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  review_id UUID,
  target_branch TEXT NOT NULL,
  commit_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  attempt INTEGER NOT NULL DEFAULT 1,
  version TEXT,
  build_id UUID,
  failure_reason TEXT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT releases_review_fkey FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  CONSTRAINT releases_build_fkey FOREIGN KEY (tenant_id, build_id) REFERENCES builds (tenant_id, build_id),
  CONSTRAINT releases_target_branch_check CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT releases_commit_id_check CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT releases_status_check CHECK (status IN ('queued', 'running', 'released', 'failed')),
  CONSTRAINT releases_attempt_check CHECK (attempt > 0),
  -- A released row must name the version it minted.
  CONSTRAINT releases_released_version_check CHECK (
    status <> 'released' OR (version IS NOT NULL AND length(trim(version)) > 0)
  ),
  -- Idempotency contract: re-triggering a released commit cannot mint a second version.
  CONSTRAINT releases_tenant_commit_key UNIQUE (tenant_id, commit_id),
  CONSTRAINT releases_tenant_release_key UNIQUE (tenant_id, release_id)
);
