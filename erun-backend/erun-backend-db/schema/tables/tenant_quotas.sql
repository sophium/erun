CREATE TABLE tenant_quotas (
  tenant_id UUID PRIMARY KEY DEFAULT erun_current_tenant_id(),
  max_environments INT NOT NULL CHECK (max_environments >= 0),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id)
);
