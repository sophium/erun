-- Listing a tenant's definitions by how recently they moved is the drift
-- sweep's access pattern: the primary key answers "what is this environment's
-- definition", this one answers "what changed across this tenant".
CREATE INDEX environment_definitions_tenant_updated_at_idx
  ON environment_definitions (tenant_id, updated_at DESC);
