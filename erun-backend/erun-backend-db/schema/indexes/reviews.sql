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

-- A review's name is the squash-merge message, so two changes that land in the
-- same repository must not claim the same one. Scoped by repository: two
-- repositories a tenant serves may each have a "Fix typo" and those are
-- different messages on different branches. A CLOSED review reserves nothing —
-- it never landed, so its name was never used as a merge message, and
-- re-opening work on a rebased branch is an ordinary workflow that would
-- otherwise have to reword its own commit subject.
--
-- NULLS NOT DISTINCT so a review with no recorded repository (created before
-- the platform recorded one) still groups with its peers by name, rather than
-- every such row being unique by virtue of the NULL.
CREATE UNIQUE INDEX reviews_tenant_repository_name_idx
  ON reviews (tenant_id, repository, name) NULLS NOT DISTINCT
  WHERE status <> 'CLOSED';

-- Branch history is unbounded (a recycled branch name may have many closed
-- reviews), but only one review may propose a given source/target pair while
-- it is still live; otherwise a second review's gate could merge a branch the
-- target already contains and mint a second release for one change. Scoped by
-- repository so the same branch name in two repositories a tenant serves are
-- two different proposals.
CREATE UNIQUE INDEX reviews_tenant_live_source_target_idx
  ON reviews (tenant_id, repository, source_branch, target_branch) NULLS NOT DISTINCT
  WHERE status NOT IN ('MERGED', 'CLOSED');
