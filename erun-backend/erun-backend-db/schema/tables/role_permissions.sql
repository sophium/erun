CREATE TABLE role_permissions (
  role_permission_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
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
