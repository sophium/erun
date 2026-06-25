CREATE TABLE context_credentials (
  context_credential_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  context_id UUID NOT NULL,
  k3s_admin_token_encrypted BYTEA NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, context_id) REFERENCES contexts (tenant_id, context_id) ON DELETE CASCADE,
  CONSTRAINT context_credentials_tenant_context_key UNIQUE (tenant_id, context_id)
);
