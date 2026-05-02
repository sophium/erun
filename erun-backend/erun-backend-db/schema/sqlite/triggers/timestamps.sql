CREATE TRIGGER tenants_set_timestamps_after_insert
  AFTER INSERT ON tenants
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE tenants
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tenants_set_timestamps_after_update
  AFTER UPDATE ON tenants
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE tenants
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE tenant_id = NEW.tenant_id;
END;

CREATE TRIGGER tenant_issuers_set_timestamps_after_insert
  AFTER INSERT ON tenant_issuers
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE tenant_issuers
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE issuer = NEW.issuer;
END;

CREATE TRIGGER tenant_issuers_set_timestamps_after_update
  AFTER UPDATE ON tenant_issuers
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE tenant_issuers
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE issuer = NEW.issuer;
END;

CREATE TRIGGER users_set_timestamps_after_insert
  AFTER INSERT ON users
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE users
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER users_set_timestamps_after_update
  AFTER UPDATE ON users
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE users
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE user_id = NEW.user_id;
END;

CREATE TRIGGER user_external_ids_set_timestamps_after_insert
  AFTER INSERT ON user_external_ids
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE user_external_ids
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE tenant_id = NEW.tenant_id
     AND issuer = NEW.issuer
     AND external_id = NEW.external_id;
END;

CREATE TRIGGER user_external_ids_set_timestamps_after_update
  AFTER UPDATE ON user_external_ids
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE user_external_ids
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE tenant_id = NEW.tenant_id
     AND issuer = NEW.issuer
     AND external_id = NEW.external_id;
END;

CREATE TRIGGER roles_set_timestamps_after_insert
  AFTER INSERT ON roles
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE roles
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER roles_set_timestamps_after_update
  AFTER UPDATE ON roles
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE roles
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE role_id = NEW.role_id;
END;

CREATE TRIGGER role_permissions_set_timestamps_after_insert
  AFTER INSERT ON role_permissions
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE role_permissions
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER role_permissions_set_timestamps_after_update
  AFTER UPDATE ON role_permissions
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE role_permissions
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE role_permission_id = NEW.role_permission_id;
END;

CREATE TRIGGER user_roles_set_timestamps_after_insert
  AFTER INSERT ON user_roles
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE user_roles
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE tenant_id = NEW.tenant_id
     AND user_id = NEW.user_id
     AND role_id = NEW.role_id;
END;

CREATE TRIGGER user_roles_set_timestamps_after_update
  AFTER UPDATE ON user_roles
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE user_roles
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE tenant_id = NEW.tenant_id
     AND user_id = NEW.user_id
     AND role_id = NEW.role_id;
END;

CREATE TRIGGER reviews_set_timestamps_after_insert
  AFTER INSERT ON reviews
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE reviews
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER reviews_set_timestamps_after_update
  AFTER UPDATE ON reviews
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE reviews
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE review_id = NEW.review_id;
END;

CREATE TRIGGER review_merge_queue_set_timestamps_after_insert
  AFTER INSERT ON review_merge_queue
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE review_merge_queue
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER review_merge_queue_set_timestamps_after_update
  AFTER UPDATE ON review_merge_queue
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE review_merge_queue
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE review_merge_queue_id = NEW.review_merge_queue_id;
END;

CREATE TRIGGER builds_set_timestamps_after_insert
  AFTER INSERT ON builds
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE builds
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER builds_set_timestamps_after_update
  AFTER UPDATE ON builds
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE builds
     SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE build_id = NEW.build_id;
END;

CREATE TRIGGER comments_set_timestamps_after_insert
  AFTER INSERT ON comments
  FOR EACH ROW
  WHEN NEW.created_at IS NULL OR NEW.updated_at IS NULL
BEGIN
  UPDATE comments
     SET created_at = COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
         updated_at = COALESCE(NEW.updated_at, COALESCE(NEW.created_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER comments_set_timestamps_after_update
  AFTER UPDATE ON comments
  FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE comments
     SET created_at = OLD.created_at,
         updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE comment_id = NEW.comment_id;
END;
