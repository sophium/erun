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
  -- Unlike reviews.author_user_id, an invite's creator is not necessarily a
  -- member of the invite's own target tenant: an OPERATIONS caller may mint
  -- an invite for a different tenant entirely (#1483 item 4), so this
  -- references the global users.user_id primary key rather than the
  -- tenant-scoped (tenant_id, user_id) composite key reviews.sql uses.
  FOREIGN KEY (created_by_user_id) REFERENCES users (user_id),
  CONSTRAINT invites_issuer_check CHECK (length(trim(issuer)) > 0),
  CONSTRAINT invites_token_check CHECK (length(trim(token)) > 0),
  CONSTRAINT invites_token_key UNIQUE (token),
  CONSTRAINT invites_tenant_invite_key UNIQUE (tenant_id, invite_id)
);
