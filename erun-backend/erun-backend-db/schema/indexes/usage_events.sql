CREATE INDEX usage_events_tenant_created_at_idx
  ON usage_events (tenant_id, created_at DESC);

CREATE INDEX usage_events_tenant_environment_idx
  ON usage_events (tenant_id, environment_id);
