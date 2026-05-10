CREATE TABLE audit_events (
  audit_event_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
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
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, erun_user_id) REFERENCES users (tenant_id, user_id),
  CONSTRAINT audit_events_type_check CHECK (type IN ('API', 'MCP', 'CLI')),
  CONSTRAINT audit_events_api_method_check CHECK (
    api_method IS NULL OR api_method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD')
  ),
  CONSTRAINT audit_events_api_fields_check CHECK (
    type <> 'API' OR (api_method IS NOT NULL AND api_path IS NOT NULL)
  ),
  CONSTRAINT audit_events_cli_fields_check CHECK (
    type <> 'CLI' OR cli_command IS NOT NULL
  ),
  CONSTRAINT audit_events_mcp_fields_check CHECK (
    type <> 'MCP' OR mcp_tool IS NOT NULL
  ),
  CONSTRAINT audit_events_tenant_event_key UNIQUE (tenant_id, audit_event_id)
);

CREATE INDEX audit_events_tenant_created_at_idx
  ON audit_events (tenant_id, created_at DESC);

CREATE INDEX audit_events_tenant_user_created_at_idx
  ON audit_events (tenant_id, erun_user_id, created_at DESC);

CREATE INDEX audit_events_tenant_api_created_at_idx
  ON audit_events (tenant_id, api_method, api_path, created_at DESC);

GRANT SELECT, INSERT, REFERENCES
  ON audit_events
  TO erun_tenant, erun_operations;

ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_events_tenant_isolation
  ON audit_events
  FOR SELECT
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id());

CREATE POLICY audit_events_tenant_insert
  ON audit_events
  FOR INSERT
  TO erun_tenant
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY audit_events_operations_select
  ON audit_events
  FOR SELECT
  TO erun_operations
  USING (true);

CREATE POLICY audit_events_operations_insert
  ON audit_events
  FOR INSERT
  TO erun_operations
  WITH CHECK (true);
