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

GRANT SELECT ON tenants, tenant_issuers, issuers TO erun_tenant;
GRANT UPDATE (name) ON tenant_issuers TO erun_tenant;
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES ON tenants, tenant_issuers, issuers TO erun_operations;

GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON users, user_external_ids, roles, role_permissions, user_roles, reviews, review_merge_queue, review_reviewers, builds, releases, comments, contexts, environments, environment_definitions, tenant_quotas, cloud_provider_aliases, context_credentials, invites, invite_requests, ai_sessions, jobs, environment_events
  TO erun_tenant, erun_operations;

GRANT SELECT, INSERT, REFERENCES
  ON audit_events, usage_events
  TO erun_tenant, erun_operations;

-- Platform-scoped singleton: any tenant reads it, only operations changes it.
GRANT SELECT ON platform_rate_limits TO erun_tenant;
GRANT SELECT, INSERT, UPDATE, REFERENCES ON platform_rate_limits TO erun_operations;

-- Platform-wide operational log, written only by the retention sweep.
GRANT SELECT, INSERT ON retention_runs TO erun_operations;
