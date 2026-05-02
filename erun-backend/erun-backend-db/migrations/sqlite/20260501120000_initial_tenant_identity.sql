CREATE TABLE tenants (
  tenant_id UUID PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL DEFAULT 'COMPANY',
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  CONSTRAINT tenants_type_check CHECK (type IN ('OPERATIONS', 'COMPANY'))
);

CREATE TABLE tenant_issuers (
  tenant_id UUID NOT NULL,
  issuer TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT tenant_issuers_tenant_issuer_key UNIQUE (tenant_id, issuer)
);

CREATE TABLE users (
  user_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  username TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT users_tenant_user_key UNIQUE (tenant_id, user_id),
  CONSTRAINT users_tenant_username_key UNIQUE (tenant_id, username)
);

CREATE TABLE user_external_ids (
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  issuer TEXT NOT NULL,
  external_id TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, issuer, external_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id),
  FOREIGN KEY (tenant_id, issuer) REFERENCES tenant_issuers (tenant_id, issuer)
);

CREATE TABLE roles (
  role_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT roles_tenant_role_key UNIQUE (tenant_id, role_id),
  CONSTRAINT roles_tenant_name_key UNIQUE (tenant_id, name)
);

CREATE TABLE role_permissions (
  role_permission_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  role_id UUID NOT NULL,
  api_method TEXT,
  api_path TEXT,
  api_method_pattern TEXT,
  api_path_pattern TEXT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, role_id),
  CONSTRAINT role_permissions_api_method_check CHECK (
    api_method IS NULL OR api_method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD')
  ),
  CONSTRAINT role_permissions_exact_or_pattern_check CHECK (
    (
      api_method IS NOT NULL
      AND api_path IS NOT NULL
      AND api_method_pattern IS NULL
      AND api_path_pattern IS NULL
    )
    OR
    (
      api_method IS NULL
      AND api_path IS NULL
      AND api_method_pattern IS NOT NULL
      AND api_path_pattern IS NOT NULL
    )
  ),
  CONSTRAINT role_permissions_tenant_permission_key UNIQUE (tenant_id, role_id, api_method, api_path),
  CONSTRAINT role_permissions_tenant_permission_pattern_key UNIQUE (tenant_id, role_id, api_method_pattern, api_path_pattern)
);

CREATE TABLE user_roles (
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  role_id UUID NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, user_id, role_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id),
  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, role_id)
);

CREATE TABLE reviews (
  review_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  name TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  source_branch TEXT NOT NULL,
  status TEXT NOT NULL,
  last_failed_build_id UUID,
  last_ready_build_id UUID,
  last_merged_build_id UUID,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT reviews_status_check CHECK (status IN ('OPEN', 'CLOSED', 'FAILED', 'READY', 'MERGE', 'MERGED')),
  CONSTRAINT reviews_target_branch_check CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT reviews_source_branch_check CHECK (length(trim(source_branch)) > 0),
  CONSTRAINT reviews_status_build_link_check CHECK (
    (status <> 'FAILED' OR last_failed_build_id IS NOT NULL)
    AND (status <> 'READY' OR last_ready_build_id IS NOT NULL)
    AND (status <> 'MERGE' OR last_ready_build_id IS NOT NULL)
    AND (status <> 'MERGED' OR last_merged_build_id IS NOT NULL)
  ),
  CONSTRAINT reviews_tenant_review_key UNIQUE (tenant_id, review_id),
  CONSTRAINT reviews_tenant_name_key UNIQUE (tenant_id, name),
  CONSTRAINT reviews_tenant_target_review_key UNIQUE (tenant_id, target_branch, review_id)
);

CREATE TABLE review_merge_queue (
  review_merge_queue_id INTEGER PRIMARY KEY,
  tenant_id UUID NOT NULL,
  target_branch TEXT NOT NULL,
  review_id UUID NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, target_branch, review_id) REFERENCES reviews (tenant_id, target_branch, review_id),
  CONSTRAINT review_merge_queue_target_branch_check CHECK (length(trim(target_branch)) > 0),
  CONSTRAINT review_merge_queue_tenant_queue_key UNIQUE (tenant_id, review_merge_queue_id),
  CONSTRAINT review_merge_queue_tenant_review_key UNIQUE (tenant_id, review_id)
);

CREATE TABLE builds (
  build_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  review_id UUID NOT NULL,
  successful BOOLEAN NOT NULL,
  commit_id TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  CONSTRAINT builds_commit_id_check CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT builds_version_check CHECK (length(trim(version)) > 0),
  CONSTRAINT builds_tenant_build_key UNIQUE (tenant_id, build_id),
  CONSTRAINT builds_tenant_review_build_key UNIQUE (tenant_id, review_id, build_id)
);

CREATE TABLE comments (
  comment_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  review_id UUID NOT NULL,
  creator_user_id UUID,
  status TEXT NOT NULL,
  parent_comment_id UUID,
  commit_id TEXT NOT NULL,
  line INTEGER NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  FOREIGN KEY (tenant_id, creator_user_id) REFERENCES users (tenant_id, user_id),
  FOREIGN KEY (tenant_id, parent_comment_id) REFERENCES comments (tenant_id, comment_id),
  CONSTRAINT comments_status_check CHECK (status IN ('OPEN', 'CLOSED')),
  CONSTRAINT comments_line_check CHECK (line > 0),
  CONSTRAINT comments_root_creator_check CHECK (parent_comment_id IS NOT NULL OR creator_user_id IS NOT NULL),
  CONSTRAINT comments_child_creator_check CHECK (parent_comment_id IS NULL OR creator_user_id IS NULL),
  CONSTRAINT comments_no_self_parent_check CHECK (parent_comment_id IS NULL OR parent_comment_id <> comment_id),
  CONSTRAINT comments_tenant_comment_key UNIQUE (tenant_id, comment_id),
  CONSTRAINT comments_tenant_review_comment_key UNIQUE (tenant_id, review_id, comment_id)
);

CREATE TABLE audit_events (
  tenant_id UUID NOT NULL,
  erun_user_id UUID NOT NULL,
  external_user_id TEXT NOT NULL,
  external_issuer_id TEXT NOT NULL,
  type TEXT NOT NULL,
  api_method TEXT,
  api_path TEXT,
  cli_command TEXT,
  cli_parameters TEXT,
  mcp_tool TEXT,
  mcp_tool_parameters TEXT,
  created_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, erun_user_id) REFERENCES users (tenant_id, user_id),
  FOREIGN KEY (tenant_id, external_issuer_id) REFERENCES tenant_issuers (tenant_id, issuer),
  FOREIGN KEY (tenant_id, external_issuer_id, external_user_id) REFERENCES user_external_ids (tenant_id, issuer, external_id),
  CONSTRAINT audit_events_type_check CHECK (type IN ('API', 'MCP', 'CLI')),
  CONSTRAINT audit_events_api_method_check CHECK (
    api_method IS NULL OR api_method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD')
  ),
  CONSTRAINT audit_events_type_shape_check CHECK (
    (
      type = 'API'
      AND api_method IS NOT NULL
      AND api_path IS NOT NULL
      AND cli_command IS NULL
      AND cli_parameters IS NULL
      AND mcp_tool IS NULL
      AND mcp_tool_parameters IS NULL
    )
    OR
    (
      type = 'CLI'
      AND api_method IS NULL
      AND api_path IS NULL
      AND cli_command IS NOT NULL
      AND mcp_tool IS NULL
      AND mcp_tool_parameters IS NULL
    )
    OR
    (
      type = 'MCP'
      AND api_method IS NULL
      AND api_path IS NULL
      AND cli_command IS NULL
      AND cli_parameters IS NULL
      AND mcp_tool IS NOT NULL
    )
  )
);

CREATE INDEX users_tenant_id_idx
  ON users (tenant_id);

CREATE INDEX user_external_ids_tenant_user_idx
  ON user_external_ids (tenant_id, user_id);

CREATE INDEX role_permissions_tenant_role_idx
  ON role_permissions (tenant_id, role_id);

CREATE INDEX role_permissions_tenant_api_idx
  ON role_permissions (tenant_id, api_method, api_path);

CREATE INDEX role_permissions_tenant_api_pattern_idx
  ON role_permissions (tenant_id, api_method_pattern, api_path_pattern);

CREATE INDEX user_roles_tenant_role_idx
  ON user_roles (tenant_id, role_id);

CREATE INDEX reviews_tenant_status_idx
  ON reviews (tenant_id, status);

CREATE INDEX reviews_tenant_target_branch_idx
  ON reviews (tenant_id, target_branch);

CREATE INDEX review_merge_queue_tenant_target_idx
  ON review_merge_queue (tenant_id, target_branch, review_merge_queue_id);

CREATE INDEX builds_tenant_review_created_at_idx
  ON builds (tenant_id, review_id, created_at);

CREATE INDEX builds_tenant_commit_idx
  ON builds (tenant_id, commit_id);

CREATE INDEX comments_tenant_review_idx
  ON comments (tenant_id, review_id);

CREATE INDEX comments_tenant_review_line_idx
  ON comments (tenant_id, review_id, commit_id, line);

CREATE INDEX comments_tenant_parent_idx
  ON comments (tenant_id, parent_comment_id);

CREATE INDEX audit_events_tenant_created_at_idx
  ON audit_events (tenant_id, created_at);

CREATE INDEX audit_events_tenant_user_created_at_idx
  ON audit_events (tenant_id, erun_user_id, created_at);

CREATE INDEX audit_events_tenant_type_created_at_idx
  ON audit_events (tenant_id, type, created_at);

CREATE INDEX audit_events_tenant_api_created_at_idx
  ON audit_events (tenant_id, api_method, api_path, created_at);

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

CREATE TRIGGER audit_events_set_created_at_after_insert
  AFTER INSERT ON audit_events
  FOR EACH ROW
  WHEN NEW.created_at IS NULL
BEGIN
  UPDATE audit_events
     SET created_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER audit_events_prevent_update
  BEFORE UPDATE ON audit_events
  FOR EACH ROW
  WHEN NOT (
    OLD.created_at IS NULL
    AND NEW.created_at IS NOT NULL
    AND NEW.tenant_id IS OLD.tenant_id
    AND NEW.erun_user_id IS OLD.erun_user_id
    AND NEW.external_user_id IS OLD.external_user_id
    AND NEW.external_issuer_id IS OLD.external_issuer_id
    AND NEW.type IS OLD.type
    AND NEW.api_method IS OLD.api_method
    AND NEW.api_path IS OLD.api_path
    AND NEW.cli_command IS OLD.cli_command
    AND NEW.cli_parameters IS OLD.cli_parameters
    AND NEW.mcp_tool IS OLD.mcp_tool
    AND NEW.mcp_tool_parameters IS OLD.mcp_tool_parameters
  )
BEGIN
  SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TRIGGER audit_events_prevent_delete
  BEFORE DELETE ON audit_events
  FOR EACH ROW
BEGIN
  SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TRIGGER tenants_set_primary_key_after_insert
  AFTER INSERT ON tenants
  FOR EACH ROW
  WHEN NEW.tenant_id IS NULL
BEGIN
  UPDATE tenants
     SET tenant_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER users_set_primary_key_after_insert
  AFTER INSERT ON users
  FOR EACH ROW
  WHEN NEW.user_id IS NULL
BEGIN
  UPDATE users
     SET user_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER roles_set_primary_key_after_insert
  AFTER INSERT ON roles
  FOR EACH ROW
  WHEN NEW.role_id IS NULL
BEGIN
  UPDATE roles
     SET role_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER role_permissions_set_primary_key_after_insert
  AFTER INSERT ON role_permissions
  FOR EACH ROW
  WHEN NEW.role_permission_id IS NULL
BEGIN
  UPDATE role_permissions
     SET role_permission_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER reviews_set_primary_key_after_insert
  AFTER INSERT ON reviews
  FOR EACH ROW
  WHEN NEW.review_id IS NULL
BEGIN
  UPDATE reviews
     SET review_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER builds_set_primary_key_after_insert
  AFTER INSERT ON builds
  FOR EACH ROW
  WHEN NEW.build_id IS NULL
BEGIN
  UPDATE builds
     SET build_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER comments_set_primary_key_after_insert
  AFTER INSERT ON comments
  FOR EACH ROW
  WHEN NEW.comment_id IS NULL
BEGIN
  UPDATE comments
     SET comment_id =
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 1, 8) || '-' ||
       substr(printf('%012x', (CAST(strftime('%s', 'now') AS INTEGER) * 1000) + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)), 9, 4) || '-' ||
       '7' || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
       substr(lower(hex(randomblob(6))), 1, 12)
   WHERE rowid = NEW.rowid;
END;




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
