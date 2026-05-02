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
