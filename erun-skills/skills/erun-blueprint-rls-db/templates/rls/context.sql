-- Set per transaction by the application (SET LOCAL erun.tenant_id); with no
-- tenant set this returns NULL, so RLS policies deny rather than match.
CREATE FUNCTION erun_current_tenant_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('erun.tenant_id', true), '')::UUID
$$;
