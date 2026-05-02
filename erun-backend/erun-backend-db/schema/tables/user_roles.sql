CREATE TABLE user_roles (
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  role_id UUID NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, user_id, role_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id),
  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, role_id)
);

