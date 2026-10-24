-- The drift sweep lists a tenant's definitions by recency.
CREATE INDEX environment_definitions_tenant_updated_at_idx
  ON environment_definitions (tenant_id, updated_at DESC);
