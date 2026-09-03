-- Per-tenant metering: one row per resource-affecting environment lifecycle
-- event (provisioned, stopped, deleted), snapshotting the resource caps that
-- applied at the time. Append-only, mirroring audit_events.
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
  -- Single-column (not tenant-scoped composite): a composite FK's ON DELETE
  -- SET NULL would null every referencing column, including the NOT NULL
  -- tenant_id. environment_id alone is enough (environments.environment_id is
  -- a globally unique UUIDv7 PK) plus RLS already scopes writes to the
  -- caller's own tenant. ON DELETE SET NULL: a deleted environment must not
  -- block on its own usage history, and the append-only metering trail should
  -- outlive the row it described (event_type and tenant_id already say what
  -- happened).
  CONSTRAINT usage_events_environment_fkey FOREIGN KEY (environment_id) REFERENCES environments (environment_id) ON DELETE SET NULL,
  CONSTRAINT usage_events_event_type_check CHECK (
    event_type IN ('environment_provisioned', 'environment_stopped', 'environment_deleted')
  ),
  CONSTRAINT usage_events_tenant_event_key UNIQUE (tenant_id, usage_event_id)
);
