-- The portable subset of an environment's config, as uploaded. A sibling table so upload
-- revisions stay independent of the lifecycle row's status churn.
CREATE TABLE environment_definitions (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  environment_id UUID NOT NULL,
  -- Incremented per upload; a counter, not a timestamp, so clients can order without a shared clock.
  revision INTEGER NOT NULL DEFAULT 1,
  -- Uninterpreted PlatformEnvDefinition; JSONB so a non-object is refused at write time.
  definition JSONB NOT NULL,
  -- Never taken from the request body; see erun_current_user_id().
  written_by_user_id UUID NOT NULL DEFAULT erun_current_user_id(),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, environment_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT environment_definitions_tenant_environment_fkey
    FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id) ON DELETE CASCADE,
  CONSTRAINT environment_definitions_revision_check CHECK (revision > 0),
  CONSTRAINT environment_definitions_definition_object_check CHECK (jsonb_typeof(definition) = 'object')
);
