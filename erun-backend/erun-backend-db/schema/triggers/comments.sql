CREATE FUNCTION erun_validate_comments()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.comment_id IS DISTINCT FROM OLD.comment_id
       OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.review_id IS DISTINCT FROM OLD.review_id
       OR NEW.creator_user_id IS DISTINCT FROM OLD.creator_user_id
       OR NEW.parent_comment_id IS DISTINCT FROM OLD.parent_comment_id
       OR NEW.commit_id IS DISTINCT FROM OLD.commit_id
       OR NEW.line IS DISTINCT FROM OLD.line THEN
      RAISE EXCEPTION 'comment thread identity fields cannot be updated';
    END IF;
  END IF;

  IF NEW.parent_comment_id IS NULL THEN
    IF EXISTS (
      SELECT 1
        FROM comments existing
       WHERE existing.tenant_id = NEW.tenant_id
         AND existing.review_id = NEW.review_id
         AND existing.commit_id = NEW.commit_id
         AND existing.line = NEW.line
         AND existing.parent_comment_id IS NULL
         AND (TG_OP = 'INSERT' OR existing.comment_id <> OLD.comment_id)
    ) THEN
      RAISE EXCEPTION 'root comment already exists for review %, commit %, line %', NEW.review_id, NEW.commit_id, NEW.line;
    END IF;
  ELSE
    IF NOT EXISTS (
      SELECT 1
        FROM comments parent
       WHERE parent.tenant_id = NEW.tenant_id
         AND parent.review_id = NEW.review_id
         AND parent.comment_id = NEW.parent_comment_id
         AND parent.commit_id = NEW.commit_id
         AND parent.line = NEW.line
         AND parent.parent_comment_id IS NULL
         AND parent.creator_user_id IS NOT NULL
    ) THEN
      RAISE EXCEPTION 'child comments must reference the root comment for the same review, commit, and line';
    END IF;
  END IF;

  IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status THEN
    IF OLD.creator_user_id IS NULL OR OLD.creator_user_id <> NULLIF(current_setting('erun.user_id', true), '')::UUID THEN
      RAISE EXCEPTION 'only the comment creator can update comment status';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER comments_validate
  BEFORE INSERT OR UPDATE ON comments
  FOR EACH ROW
  EXECUTE FUNCTION erun_validate_comments();
