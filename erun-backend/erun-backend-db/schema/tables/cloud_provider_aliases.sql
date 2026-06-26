CREATE TABLE cloud_provider_aliases (
  cloud_provider_alias_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  alias TEXT NOT NULL,
  provider TEXT NOT NULL CHECK (provider IN ('aws')),
  credentials_encrypted BYTEA NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT cloud_provider_aliases_tenant_alias_key UNIQUE (tenant_id, alias)
);
