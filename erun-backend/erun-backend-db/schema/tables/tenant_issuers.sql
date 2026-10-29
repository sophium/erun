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
  -- A tenant maps a given issuer once. UNIQUE, not PK, because
  -- user_external_ids' (tenant_id, issuer) FK depends on this constraint.
  CONSTRAINT tenant_issuers_tenant_issuer_key UNIQUE (tenant_id, issuer),
  -- A token's (iss, org-claim value) resolves to one tenant; NULLS NOT DISTINCT
  -- covers a single-tenant issuer (NULL org).
  CONSTRAINT tenant_issuers_issuer_org_key UNIQUE NULLS NOT DISTINCT (issuer, org_field_value)
);
