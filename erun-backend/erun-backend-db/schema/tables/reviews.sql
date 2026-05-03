CREATE TABLE reviews (
  review_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  name TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  source_branch TEXT NOT NULL,
  status TEXT NOT NULL,
  last_failed_build_id UUID,
  last_ready_build_id UUID,
  last_merged_build_id UUID,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT reviews_status_check CHECK (status IN ('OPEN', 'CLOSED', 'FAILED', 'READY', 'MERGE', 'MERGED')),
  CONSTRAINT reviews_target_branch_check CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT reviews_source_branch_check CHECK (length(trim(source_branch)) > 0),
  CONSTRAINT reviews_status_build_link_check CHECK (
    (status <> 'FAILED' OR last_failed_build_id IS NOT NULL)
    AND (status <> 'READY' OR last_ready_build_id IS NOT NULL)
    AND (status <> 'MERGE' OR last_ready_build_id IS NOT NULL)
    AND (status <> 'MERGED' OR last_merged_build_id IS NOT NULL)
  ),
  CONSTRAINT reviews_tenant_review_key UNIQUE (tenant_id, review_id),
  CONSTRAINT reviews_tenant_name_key UNIQUE (tenant_id, name),
  CONSTRAINT reviews_tenant_target_review_key UNIQUE (tenant_id, target_branch, review_id)
);
