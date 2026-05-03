CREATE TABLE users (
  user_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  username TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT users_tenant_user_key UNIQUE (tenant_id, user_id),
  CONSTRAINT users_tenant_username_key UNIQUE (tenant_id, username)
);
