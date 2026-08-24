CREATE INDEX comments_tenant_review_idx
  ON comments (tenant_id, review_id);

CREATE INDEX comments_tenant_review_line_idx
  ON comments (tenant_id, review_id, commit_id, file_path, line);

CREATE INDEX comments_tenant_parent_idx
  ON comments (tenant_id, parent_comment_id);

