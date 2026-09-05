-- Per-tenant CPU/memory/storage caps (env count already existed) and a
-- metering table recording per-tenant environment lifecycle usage events.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/tenant_quotas.sql, schema/tables/usage_events.sql,
-- schema/indexes/usage_events.sql and schema/rls/usage_events.sql.

ALTER TABLE "tenant_quotas"
  ADD COLUMN "max_cpu_millicores" integer NOT NULL DEFAULT 4000,
  ADD COLUMN "max_memory_mb" integer NOT NULL DEFAULT 9216,
  ADD COLUMN "max_storage_gb" integer NOT NULL DEFAULT 80;

ALTER TABLE "tenant_quotas"
  ADD CONSTRAINT "tenant_quotas_max_cpu_millicores_check" CHECK (max_cpu_millicores >= 0),
  ADD CONSTRAINT "tenant_quotas_max_memory_mb_check" CHECK (max_memory_mb >= 0),
  ADD CONSTRAINT "tenant_quotas_max_storage_gb_check" CHECK (max_storage_gb >= 0);

CREATE TABLE "usage_events" (
  "usage_event_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "environment_id" uuid NULL,
  "event_type" text NOT NULL,
  "cpu_millicores" integer NULL,
  "memory_mb" integer NULL,
  "storage_gb" integer NULL,
  "created_at" timestamptz NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("usage_event_id"),
  CONSTRAINT "usage_events_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "usage_events_environment_fkey" FOREIGN KEY ("environment_id") REFERENCES "environments" ("environment_id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "usage_events_event_type_check" CHECK (
    event_type IN ('environment_provisioned', 'environment_stopped', 'environment_deleted')
  ),
  CONSTRAINT "usage_events_tenant_event_key" UNIQUE ("tenant_id", "usage_event_id")
);

CREATE INDEX "usage_events_tenant_created_at_idx" ON "usage_events" ("tenant_id", "created_at" DESC);
CREATE INDEX "usage_events_tenant_environment_idx" ON "usage_events" ("tenant_id", "environment_id");

GRANT SELECT, INSERT, REFERENCES
  ON "usage_events"
  TO erun_tenant, erun_operations;

ALTER TABLE "usage_events" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "usage_events" FORCE ROW LEVEL SECURITY;

CREATE POLICY usage_events_tenant_isolation
  ON "usage_events"
  FOR SELECT
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id());

CREATE POLICY usage_events_tenant_insert
  ON "usage_events"
  FOR INSERT
  TO erun_tenant
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY usage_events_operations_select
  ON "usage_events"
  FOR SELECT
  TO erun_operations
  USING (true);

CREATE POLICY usage_events_operations_insert
  ON "usage_events"
  FOR INSERT
  TO erun_operations
  WITH CHECK (true);
