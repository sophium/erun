CREATE TABLE tenants (
  tenant_id UUID PRIMARY KEY DEFAULT uuidv7(),
  name TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL DEFAULT 'COMPANY',
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  CONSTRAINT tenants_type_check CHECK (type IN ('OPERATIONS', 'COMPANY'))
);
