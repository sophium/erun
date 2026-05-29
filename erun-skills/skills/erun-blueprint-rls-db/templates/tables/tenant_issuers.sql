CREATE TABLE tenant_issuers (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  issuer TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT tenant_issuers_name_check CHECK (length(trim(name)) > 0),
  CONSTRAINT tenant_issuers_tenant_issuer_key UNIQUE (tenant_id, issuer)
);
