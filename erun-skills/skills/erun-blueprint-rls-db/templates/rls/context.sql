-- erun_current_tenant_id() returns the tenant context for the current
-- PostgreSQL session, or NULL if no tenant has been set. Used by
-- tenant-owned table defaults (tenant_id DEFAULT erun_current_tenant_id())
-- and by RLS policies (USING (tenant_id = erun_current_tenant_id())).
--
-- Set by the application before tenant-owned queries:
--   SET LOCAL ROLE erun_tenant;
--   SET LOCAL erun.tenant_id = '<uuid>';
--
-- If no tenant is set, this returns NULL and RLS policies deny access
-- rather than matching rows.
CREATE FUNCTION erun_current_tenant_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('erun.tenant_id', true), '')::UUID
$$;
