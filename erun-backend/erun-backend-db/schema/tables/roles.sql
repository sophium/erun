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

