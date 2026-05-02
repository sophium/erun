CREATE SEQUENCE review_merge_queue_id_seq;

CREATE FUNCTION erun_set_primary_keys()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_TABLE_NAME = 'tenants' THEN
    NEW.tenant_id = COALESCE(NEW.tenant_id, uuidv7());
  ELSIF TG_TABLE_NAME = 'users' THEN
    NEW.user_id = COALESCE(NEW.user_id, uuidv7());
  ELSIF TG_TABLE_NAME = 'roles' THEN
    NEW.role_id = COALESCE(NEW.role_id, uuidv7());
  ELSIF TG_TABLE_NAME = 'role_permissions' THEN
    NEW.role_permission_id = COALESCE(NEW.role_permission_id, uuidv7());
  ELSIF TG_TABLE_NAME = 'reviews' THEN
    NEW.review_id = COALESCE(NEW.review_id, uuidv7());
  ELSIF TG_TABLE_NAME = 'review_merge_queue' THEN
    NEW.review_merge_queue_id = COALESCE(NEW.review_merge_queue_id, nextval('review_merge_queue_id_seq'));
  ELSIF TG_TABLE_NAME = 'builds' THEN
    NEW.build_id = COALESCE(NEW.build_id, uuidv7());
  ELSIF TG_TABLE_NAME = 'comments' THEN
    NEW.comment_id = COALESCE(NEW.comment_id, uuidv7());
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_set_primary_keys
  BEFORE INSERT ON tenants
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER users_set_primary_keys
  BEFORE INSERT ON users
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER roles_set_primary_keys
  BEFORE INSERT ON roles
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER role_permissions_set_primary_keys
  BEFORE INSERT ON role_permissions
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER reviews_set_primary_keys
  BEFORE INSERT ON reviews
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER review_merge_queue_set_primary_keys
  BEFORE INSERT ON review_merge_queue
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER builds_set_primary_keys
  BEFORE INSERT ON builds
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();

CREATE TRIGGER comments_set_primary_keys
  BEFORE INSERT ON comments
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_primary_keys();
