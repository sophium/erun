CREATE TRIGGER reviews_validate_build_links_before_update
  BEFORE UPDATE ON reviews
  FOR EACH ROW
  WHEN (
    NEW.last_failed_build_id IS NOT NULL
    AND NOT EXISTS (
      SELECT 1
        FROM builds
       WHERE tenant_id = NEW.tenant_id
         AND review_id = NEW.review_id
         AND build_id = NEW.last_failed_build_id
    )
  )
  OR (
    NEW.last_ready_build_id IS NOT NULL
    AND NOT EXISTS (
      SELECT 1
        FROM builds
       WHERE tenant_id = NEW.tenant_id
         AND review_id = NEW.review_id
         AND build_id = NEW.last_ready_build_id
    )
  )
  OR (
    NEW.last_merged_build_id IS NOT NULL
    AND NOT EXISTS (
      SELECT 1
        FROM builds
       WHERE tenant_id = NEW.tenant_id
         AND review_id = NEW.review_id
         AND build_id = NEW.last_merged_build_id
    )
  )
BEGIN
  SELECT RAISE(ABORT, 'review last build id must reference a build for the same review');
END;
