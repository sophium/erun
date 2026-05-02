CREATE INDEX review_merge_queue_tenant_target_idx
  ON review_merge_queue (tenant_id, target_branch, review_merge_queue_id);
