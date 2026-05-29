DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'erun_tenant') THEN
    CREATE ROLE erun_tenant NOLOGIN;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'erun_operations') THEN
    CREATE ROLE erun_operations NOLOGIN;
  END IF;
END;
$$;

GRANT USAGE ON SCHEMA public TO erun_tenant, erun_operations;

-- Root tenant resolution tables — tightly scoped grants. erun_tenant can
-- read its own tenant and update its issuer display name; erun_operations
-- has full access.
GRANT SELECT ON tenants, tenant_issuers TO erun_tenant;
GRANT UPDATE (name) ON tenant_issuers TO erun_tenant;
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES ON tenants, tenant_issuers TO erun_operations;

-- Tenant-owned tables — both roles get full CRUD; RLS policies enforce
-- the boundary. Add new tenant-owned tables to this list as they are
-- created.
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON users, user_external_ids
  TO erun_tenant, erun_operations;
