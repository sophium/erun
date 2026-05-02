CREATE TABLE tenant_issuers (
  tenant_id UUID NOT NULL,
  issuer TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT tenant_issuers_tenant_issuer_key UNIQUE (tenant_id, issuer)
);
