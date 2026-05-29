CREATE TABLE user_external_ids (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  user_id UUID NOT NULL,
  issuer TEXT NOT NULL,
  external_id TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, issuer, external_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id),
  FOREIGN KEY (tenant_id, issuer) REFERENCES tenant_issuers (tenant_id, issuer)
);
