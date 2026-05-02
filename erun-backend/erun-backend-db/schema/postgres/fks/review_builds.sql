ALTER TABLE reviews
  ADD CONSTRAINT reviews_last_failed_build_fk
  FOREIGN KEY (tenant_id, review_id, last_failed_build_id)
  REFERENCES builds (tenant_id, review_id, build_id);

ALTER TABLE reviews
  ADD CONSTRAINT reviews_last_ready_build_fk
  FOREIGN KEY (tenant_id, review_id, last_ready_build_id)
  REFERENCES builds (tenant_id, review_id, build_id);

ALTER TABLE reviews
  ADD CONSTRAINT reviews_last_merged_build_fk
  FOREIGN KEY (tenant_id, review_id, last_merged_build_id)
  REFERENCES builds (tenant_id, review_id, build_id);
