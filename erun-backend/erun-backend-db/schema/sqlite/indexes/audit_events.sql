CREATE INDEX audit_events_tenant_created_at_idx
  ON audit_events (tenant_id, created_at);

CREATE INDEX audit_events_tenant_user_created_at_idx
  ON audit_events (tenant_id, erun_user_id, created_at);

CREATE INDEX audit_events_tenant_type_created_at_idx
  ON audit_events (tenant_id, type, created_at);

CREATE INDEX audit_events_tenant_api_created_at_idx
  ON audit_events (tenant_id, api_method, api_path, created_at);
