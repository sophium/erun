-- Comments had no body, no file path, and could not attribute a reply to its
-- author. Adding file_path NOT NULL, body NOT NULL, and creator_user_id NOT
-- NULL without a DEFAULT means this migration refuses outright on any
-- existing row that lacks a value it cannot backfill (no shipped client has
-- ever written a comment, so the table is expected to be empty).
ALTER TABLE comments
  ADD COLUMN file_path TEXT NOT NULL,
  ADD COLUMN body TEXT NOT NULL;

ALTER TABLE comments
  ALTER COLUMN creator_user_id SET NOT NULL;

ALTER TABLE comments
  ADD CONSTRAINT comments_file_path_check CHECK (file_path <> ''),
  ADD CONSTRAINT comments_body_check CHECK (btrim(body) <> '' AND octet_length(body) <= 8192);

ALTER TABLE comments
  DROP CONSTRAINT comments_root_creator_check,
  DROP CONSTRAINT comments_child_creator_check;

DROP INDEX comments_tenant_review_line_idx;

CREATE INDEX comments_tenant_review_line_idx
  ON comments (tenant_id, review_id, commit_id, file_path, line);

CREATE OR REPLACE FUNCTION erun_validate_comments()
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
       OR NEW.file_path IS DISTINCT FROM OLD.file_path
       OR NEW.line IS DISTINCT FROM OLD.line
       OR NEW.body IS DISTINCT FROM OLD.body THEN
      RAISE EXCEPTION 'comment thread identity fields cannot be updated'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF NEW.parent_comment_id IS NULL THEN
    IF EXISTS (
      SELECT 1
        FROM comments existing
       WHERE existing.tenant_id = NEW.tenant_id
         AND existing.review_id = NEW.review_id
         AND existing.commit_id = NEW.commit_id
         AND existing.file_path = NEW.file_path
         AND existing.line = NEW.line
         AND existing.parent_comment_id IS NULL
         AND (TG_OP = 'INSERT' OR existing.comment_id <> OLD.comment_id)
    ) THEN
      RAISE EXCEPTION 'root comment already exists for review %, commit %, file %, line %', NEW.review_id, NEW.commit_id, NEW.file_path, NEW.line
        USING ERRCODE = 'unique_violation';
    END IF;
  ELSE
    IF NOT EXISTS (
      SELECT 1
        FROM comments parent
       WHERE parent.tenant_id = NEW.tenant_id
         AND parent.review_id = NEW.review_id
         AND parent.comment_id = NEW.parent_comment_id
         AND parent.commit_id = NEW.commit_id
         AND parent.file_path = NEW.file_path
         AND parent.line = NEW.line
         AND parent.parent_comment_id IS NULL
    ) THEN
      RAISE EXCEPTION 'child comments must reference the root comment for the same review, commit, file, and line'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status THEN
    IF OLD.parent_comment_id IS NOT NULL THEN
      RAISE EXCEPTION 'only the root comment of a thread can have its status updated'
        USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.creator_user_id <> NULLIF(current_setting('erun.user_id', true), '')::UUID THEN
      RAISE EXCEPTION 'only the root comment creator can update comment status'
        USING ERRCODE = 'insufficient_privilege';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;
