CREATE TABLE tenant_issuers (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  issuer TEXT NOT NULL,
  org_field_value TEXT,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (issuer) REFERENCES issuers (issuer),
  CONSTRAINT tenant_issuers_name_check CHECK (length(trim(name)) > 0),
  -- Identity is (tenant_id, issuer): a tenant maps a given issuer once. Kept as
  -- a UNIQUE (not promoted to PK) so user_external_ids' (tenant_id, issuer) FK
  -- keeps depending on this exact constraint and the migration needn't rebuild
  -- it. Replaces the old global `issuer` PK so one issuer can serve many tenants.
  CONSTRAINT tenant_issuers_tenant_issuer_key UNIQUE (tenant_id, issuer),
  -- Resolution key: a token's (iss, org-claim value) maps to exactly one tenant.
  -- NULLS NOT DISTINCT so a single-tenant issuer (NULL org) maps to one tenant.
  CONSTRAINT tenant_issuers_issuer_org_key UNIQUE NULLS NOT DISTINCT (issuer, org_field_value)
);
