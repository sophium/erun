CREATE TABLE users (
  user_id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL,
  username TEXT NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT users_tenant_user_key UNIQUE (tenant_id, user_id),
  CONSTRAINT users_tenant_username_key UNIQUE (tenant_id, username)
);
