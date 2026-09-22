CREATE INDEX reviews_tenant_status_idx
  ON reviews (tenant_id, status);

CREATE INDEX reviews_tenant_target_branch_idx
  ON reviews (tenant_id, target_branch);

CREATE INDEX reviews_tenant_author_idx
  ON reviews (tenant_id, author_user_id);

CREATE INDEX reviews_tenant_source_branch_idx
  ON reviews (tenant_id, source_branch);

-- A review's name is the squash-merge message, so two changes that land must
-- not claim the same one. Only a review that can still reach MERGED, or did
-- reach it, reserves it: a CLOSED review never landed, so its name was never
-- used as a merge message and holds nothing. Re-opening work on a rebased
-- branch is an ordinary workflow — a rebase, a re-do, a review abandoned and
-- picked up again — and in every one of those the natural name for the new
-- review is the one the dead review was created with.
CREATE UNIQUE INDEX reviews_tenant_name_idx
  ON reviews (tenant_id, name)
  WHERE status <> 'CLOSED';

-- Branch history is unbounded (a recycled branch name may have many closed
-- reviews), but only one review may propose a given source/target pair while
-- it is still live; otherwise a second review's gate could merge a branch the
-- target already contains and mint a second release for one change.
CREATE UNIQUE INDEX reviews_tenant_live_source_target_idx
  ON reviews (tenant_id, source_branch, target_branch)
  WHERE status NOT IN ('MERGED', 'CLOSED');
