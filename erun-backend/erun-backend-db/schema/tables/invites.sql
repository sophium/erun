CREATE TABLE invites (
  invite_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  created_by_user_id UUID NOT NULL DEFAULT erun_current_user_id(),
  issuer TEXT NOT NULL,
  token TEXT NOT NULL,
  email TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  -- Global users key: an OPERATIONS caller may mint an invite for another tenant.
  FOREIGN KEY (created_by_user_id) REFERENCES users (user_id),
  CONSTRAINT invites_issuer_check CHECK (length(trim(issuer)) > 0),
  CONSTRAINT invites_token_check CHECK (length(trim(token)) > 0),
  CONSTRAINT invites_token_key UNIQUE (token),
  CONSTRAINT invites_tenant_invite_key UNIQUE (tenant_id, invite_id)
);
