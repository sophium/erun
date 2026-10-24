-- Append-only per-tenant metering: one row per environment lifecycle event,
-- snapshotting the resource caps then in force.
CREATE TABLE usage_events (
  usage_event_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  environment_id UUID,
  event_type TEXT NOT NULL,
  cpu_millicores INT,
  memory_mb INT,
  storage_gb INT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  -- Single-column: a composite FK's SET NULL would null the NOT NULL tenant_id.
  -- SET NULL so the metering trail outlives the deleted environment.
  CONSTRAINT usage_events_environment_fkey FOREIGN KEY (environment_id) REFERENCES environments (environment_id) ON DELETE SET NULL,
  CONSTRAINT usage_events_event_type_check CHECK (
    event_type IN ('environment_provisioned', 'environment_stopped', 'environment_deleted')
  ),
  CONSTRAINT usage_events_tenant_event_key UNIQUE (tenant_id, usage_event_id)
);
