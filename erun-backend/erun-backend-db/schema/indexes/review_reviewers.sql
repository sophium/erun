CREATE INDEX review_reviewers_tenant_user_idx
  ON review_reviewers (tenant_id, user_id, review_id);
