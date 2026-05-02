CREATE TABLE builds (
  build_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  review_id UUID NOT NULL,
  successful BOOLEAN NOT NULL,
  commit_id TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  CONSTRAINT builds_commit_id_check CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT builds_version_check CHECK (length(trim(version)) > 0),
  CONSTRAINT builds_tenant_build_key UNIQUE (tenant_id, build_id),
  CONSTRAINT builds_tenant_review_build_key UNIQUE (tenant_id, review_id, build_id)
);
