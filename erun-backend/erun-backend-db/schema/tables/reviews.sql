CREATE TABLE reviews (
  review_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  author_user_id UUID NOT NULL DEFAULT erun_current_user_id(),
  -- Canonical remote identity (RepositoryIdentity); a tenant may serve several
  -- repositories. NULL only on reviews that predate recording it.
  repository TEXT,
  name TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  source_branch TEXT NOT NULL,
  -- Canonical owner/repo#number, as jobs.issue_ref; NULL when undeclared. Never
  -- re-derived from the branch, so a rename cannot move a review off its issue.
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
  -- MERGED is deliberately absent: a review that landed outside the queue has no
  -- build to name. ReviewService.acceptMerged checks queue-driven MERGED reports.
  CONSTRAINT reviews_status_build_link_check CHECK (
    (status <> 'FAILED' OR last_failed_build_id IS NOT NULL)
    AND (status <> 'READY' OR last_ready_build_id IS NOT NULL)
    AND (status <> 'MERGE' OR last_ready_build_id IS NOT NULL)
  ),
  CONSTRAINT reviews_tenant_review_key UNIQUE (tenant_id, review_id),
  CONSTRAINT reviews_tenant_target_review_key UNIQUE (tenant_id, target_branch, review_id)
);
