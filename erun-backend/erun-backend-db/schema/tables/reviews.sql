CREATE TABLE reviews (
  review_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  author_user_id UUID NOT NULL DEFAULT erun_current_user_id(),
  -- The repository whose branches this review proposes to merge, as a
  -- canonical remote identity (erun-common's RepositoryIdentity). NULL only
  -- for reviews created before the platform recorded one: a tenant may serve
  -- more than one repository, and without this a review's source/target
  -- branch pair names the repository only by convention.
  repository TEXT,
  name TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  source_branch TEXT NOT NULL,
  -- issue_ref is the issue this review's work belongs to, in the canonical
  -- owner/repo#number spelling (jobs.issue_ref uses the same one). NULL when
  -- the author declared none, which is a review whose link, if any, is the
  -- bare number its source branch names under the branch convention. The
  -- declared value is what it is: it is never re-derived on read, so a
  -- branch renamed after creation cannot move a review off the issue its
  -- author recorded.
  issue_ref TEXT,
  status TEXT NOT NULL,
  last_failed_build_id UUID,
  last_ready_build_id UUID,
  last_merged_build_id UUID,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, author_user_id) REFERENCES users (tenant_id, user_id),
  CONSTRAINT reviews_status_check CHECK (status IN ('OPEN', 'CLOSED', 'FAILED', 'READY', 'MERGE', 'MERGED')),
  CONSTRAINT reviews_target_branch_check CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT reviews_source_branch_check CHECK (length(trim(source_branch)) > 0),
  CONSTRAINT reviews_repository_check CHECK (repository IS NULL OR length(trim(repository)) > 0),
  CONSTRAINT reviews_issue_ref_check CHECK (issue_ref IS NULL OR length(trim(issue_ref)) > 0),
  -- MERGED is deliberately not in this list. A review reconciled against the
  -- target branch's own history after landing outside the queue has no build to
  -- name, and recording none is what keeps gatedTargetTip anchored on a real
  -- build instead of an empty id; the build a MERGED report does have to name
  -- when the queue drove it is checked where it can be read, in
  -- ReviewService.acceptMerged.
  CONSTRAINT reviews_status_build_link_check CHECK (
    (status <> 'FAILED' OR last_failed_build_id IS NOT NULL)
    AND (status <> 'READY' OR last_ready_build_id IS NOT NULL)
    AND (status <> 'MERGE' OR last_ready_build_id IS NOT NULL)
  ),
  CONSTRAINT reviews_tenant_review_key UNIQUE (tenant_id, review_id),
  CONSTRAINT reviews_tenant_target_review_key UNIQUE (tenant_id, target_branch, review_id)
);
