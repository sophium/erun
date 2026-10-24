CREATE INDEX reviews_tenant_status_idx
  ON reviews (tenant_id, status);

CREATE INDEX reviews_tenant_target_branch_idx
  ON reviews (tenant_id, target_branch);

CREATE INDEX reviews_tenant_author_idx
  ON reviews (tenant_id, author_user_id);

CREATE INDEX reviews_tenant_source_branch_idx
  ON reviews (tenant_id, source_branch);

-- Finding a tenant's reviews for one repository (the merge queue's own key).
CREATE INDEX reviews_tenant_repository_idx
  ON reviews (tenant_id, repository);

-- A review's name is its squash-merge message, so it is unique per repository among reviews
-- that may still land; NULLS NOT DISTINCT groups rows with no recorded repository.
CREATE UNIQUE INDEX reviews_tenant_repository_name_idx
  ON reviews (tenant_id, repository, name) NULLS NOT DISTINCT
  WHERE status <> 'CLOSED';

-- One live review per source/target pair, or a second gate could re-merge a landed branch
-- and mint a second release.
CREATE UNIQUE INDEX reviews_tenant_live_source_target_idx
  ON reviews (tenant_id, repository, source_branch, target_branch) NULLS NOT DISTINCT
  WHERE status NOT IN ('MERGED', 'CLOSED');
