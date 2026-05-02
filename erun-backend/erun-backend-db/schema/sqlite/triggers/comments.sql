CREATE TRIGGER comments_validate_root_before_insert
  BEFORE INSERT ON comments
  FOR EACH ROW
  WHEN NEW.parent_comment_id IS NULL
   AND EXISTS (
    SELECT 1
      FROM comments existing
     WHERE existing.tenant_id = NEW.tenant_id
       AND existing.review_id = NEW.review_id
       AND existing.commit_id = NEW.commit_id
       AND existing.line = NEW.line
       AND existing.parent_comment_id IS NULL
  )
BEGIN
  SELECT RAISE(ABORT, 'root comment already exists for review commit line');
END;

CREATE TRIGGER comments_validate_immutable_fields_before_update
  BEFORE UPDATE ON comments
  FOR EACH ROW
  WHEN (NEW.comment_id IS NOT OLD.comment_id AND NOT (OLD.comment_id IS NULL AND NEW.comment_id IS NOT NULL))
    OR NEW.tenant_id IS NOT OLD.tenant_id
    OR NEW.review_id IS NOT OLD.review_id
    OR NEW.creator_user_id IS NOT OLD.creator_user_id
    OR NEW.parent_comment_id IS NOT OLD.parent_comment_id
    OR NEW.commit_id IS NOT OLD.commit_id
    OR NEW.line IS NOT OLD.line
BEGIN
  SELECT RAISE(ABORT, 'comment thread identity fields cannot be updated');
END;

CREATE TRIGGER comments_validate_root_before_update
  BEFORE UPDATE ON comments
  FOR EACH ROW
  WHEN NEW.parent_comment_id IS NULL
   AND EXISTS (
    SELECT 1
      FROM comments existing
     WHERE existing.tenant_id = NEW.tenant_id
       AND existing.review_id = NEW.review_id
       AND existing.commit_id = NEW.commit_id
       AND existing.line = NEW.line
       AND existing.parent_comment_id IS NULL
       AND existing.comment_id <> OLD.comment_id
  )
BEGIN
  SELECT RAISE(ABORT, 'root comment already exists for review commit line');
END;

CREATE TRIGGER comments_validate_child_before_insert
  BEFORE INSERT ON comments
  FOR EACH ROW
  WHEN NEW.parent_comment_id IS NOT NULL
   AND NOT EXISTS (
    SELECT 1
      FROM comments parent
     WHERE parent.tenant_id = NEW.tenant_id
       AND parent.review_id = NEW.review_id
       AND parent.comment_id = NEW.parent_comment_id
       AND parent.commit_id = NEW.commit_id
       AND parent.line = NEW.line
       AND parent.parent_comment_id IS NULL
       AND parent.creator_user_id IS NOT NULL
  )
BEGIN
  SELECT RAISE(ABORT, 'child comments must reference the root comment for the same review commit line');
END;

CREATE TRIGGER comments_validate_child_before_update
  BEFORE UPDATE ON comments
  FOR EACH ROW
  WHEN NEW.parent_comment_id IS NOT NULL
   AND NOT EXISTS (
    SELECT 1
      FROM comments parent
     WHERE parent.tenant_id = NEW.tenant_id
       AND parent.review_id = NEW.review_id
       AND parent.comment_id = NEW.parent_comment_id
       AND parent.commit_id = NEW.commit_id
       AND parent.line = NEW.line
       AND parent.parent_comment_id IS NULL
       AND parent.creator_user_id IS NOT NULL
  )
BEGIN
  SELECT RAISE(ABORT, 'child comments must reference the root comment for the same review commit line');
END;
