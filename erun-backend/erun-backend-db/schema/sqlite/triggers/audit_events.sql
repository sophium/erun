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
