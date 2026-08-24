CREATE INDEX reviews_tenant_status_idx
  ON reviews (tenant_id, status);

CREATE INDEX reviews_tenant_target_branch_idx
  ON reviews (tenant_id, target_branch);

CREATE INDEX reviews_tenant_author_idx
  ON reviews (tenant_id, author_user_id);

CREATE INDEX reviews_tenant_source_branch_idx
  ON reviews (tenant_id, source_branch);

-- Branch history is unbounded (a recycled branch name may have many closed
-- reviews), but only one review may propose a given source/target pair while
-- it is still live; otherwise a second review's gate could merge a branch the
-- target already contains and mint a second release for one change.
CREATE UNIQUE INDEX reviews_tenant_live_source_target_idx
  ON reviews (tenant_id, source_branch, target_branch)
  WHERE status NOT IN ('MERGED', 'CLOSED');
