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
