CREATE TABLE audit_events (
  tenant_id UUID,
  erun_user_id UUID,
  external_user_id String,
  external_issuer_id String,
  type LowCardinality(String),
  api_method Nullable(String),
  api_path Nullable(String),
  cli_command Nullable(String),
  cli_parameters Nullable(String),
  mcp_tool Nullable(String),
  mcp_tool_parameters Nullable(String),
  created_at DateTime64(3, 'UTC') DEFAULT now64(3),
  CONSTRAINT audit_events_type_check CHECK type IN ('API', 'MCP', 'CLI'),
  CONSTRAINT audit_events_api_method_check CHECK isNull(api_method) OR api_method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD')
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (tenant_id, created_at, erun_user_id, type);
