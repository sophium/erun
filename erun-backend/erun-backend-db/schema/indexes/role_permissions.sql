CREATE INDEX role_permissions_tenant_role_idx
  ON role_permissions (tenant_id, role_id);

CREATE INDEX role_permissions_tenant_api_idx
  ON role_permissions (tenant_id, api_method, api_path);

CREATE INDEX role_permissions_tenant_api_pattern_idx
  ON role_permissions (tenant_id, api_method_pattern, api_path_pattern);
