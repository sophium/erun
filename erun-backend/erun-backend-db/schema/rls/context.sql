CREATE FUNCTION erun_current_tenant_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('erun.tenant_id', true), '')::UUID
$$;

CREATE FUNCTION erun_current_user_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('erun.user_id', true), '')::UUID
$$;
