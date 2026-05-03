CREATE FUNCTION erun_current_tenant_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('erun.tenant_id', true), '')::UUID
$$;
