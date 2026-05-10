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
