-- Create "contexts" table
CREATE TABLE "contexts" (
  "context_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "name" text NOT NULL,
  "provider" text NOT NULL,
  "cloud_provider_alias" text NULL,
  "region" text NULL,
  "instance_id" text NULL,
  "public_ip" text NULL,
  "instance_type" text NULL,
  "disk_type" text NULL,
  "disk_size_gb" integer NULL,
  "kubernetes_context" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("context_id"),
  CONSTRAINT "contexts_tenant_context_key" UNIQUE ("tenant_id", "context_id"),
  CONSTRAINT "contexts_tenant_name_key" UNIQUE ("tenant_id", "name"),
  CONSTRAINT "contexts_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "contexts_name_check" CHECK (length(TRIM(BOTH FROM name)) > 0),
  CONSTRAINT "contexts_provider_check" CHECK (length(TRIM(BOTH FROM provider)) > 0)
);
-- Create index "contexts_tenant_provider_idx" to table: "contexts"
CREATE INDEX "contexts_tenant_provider_idx" ON "contexts" ("tenant_id", "provider");
-- Create "environments" table
CREATE TABLE "environments" (
  "environment_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "name" text NOT NULL,
  "type" text NOT NULL,
  "kubernetes_context" text NULL,
  "context_id" uuid NULL,
  "runtime_version" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("environment_id"),
  CONSTRAINT "environments_tenant_environment_key" UNIQUE ("tenant_id", "environment_id"),
  CONSTRAINT "environments_tenant_name_key" UNIQUE ("tenant_id", "name"),
  CONSTRAINT "environments_tenant_id_context_id_fkey" FOREIGN KEY ("tenant_id", "context_id") REFERENCES "contexts" ("tenant_id", "context_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "environments_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "environments_name_check" CHECK (length(TRIM(BOTH FROM name)) > 0),
  CONSTRAINT "environments_type_check" CHECK (type = ANY (ARRAY['local-agent'::text, 'remote-agent'::text, 'runtime'::text]))
);
-- Create index "environments_tenant_context_idx" to table: "environments"
CREATE INDEX "environments_tenant_context_idx" ON "environments" ("tenant_id", "context_id");
-- Create index "environments_tenant_type_idx" to table: "environments"
CREATE INDEX "environments_tenant_type_idx" ON "environments" ("tenant_id", "type");
-- Timestamp triggers for the new tables (atlas migrate diff omits trigger
-- objects without an atlas login session; appended from the declarative
-- schema; references the existing erun_set_timestamps()).
CREATE TRIGGER contexts_set_timestamps
  BEFORE INSERT OR UPDATE ON "contexts"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
CREATE TRIGGER environments_set_timestamps
  BEFORE INSERT OR UPDATE ON "environments"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
-- Grant tenant-owned CRUD on the new tables to the application roles
-- (atlas migrate diff omits permissions without an atlas login session;
-- appended from schema/roles.sql). erun_tenant gets RLS-scoped access;
-- erun_operations crosses tenants under its own policies.
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "contexts", "environments"
  TO erun_tenant, erun_operations;
-- Row-level security for the new tenant-owned tables (atlas migrate diff omits
-- policies without an atlas login session; appended from schema/rls/contexts.sql
-- and schema/rls/environments.sql). Two policies per table mirror the reviews
-- template: tenant_isolation scopes erun_tenant by erun_current_tenant_id();
-- operations_access lets erun_operations cross tenants.
ALTER TABLE "contexts" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "contexts" FORCE ROW LEVEL SECURITY;
CREATE POLICY contexts_tenant_isolation
  ON "contexts"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY contexts_operations_access
  ON "contexts"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
ALTER TABLE "environments" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "environments" FORCE ROW LEVEL SECURITY;
CREATE POLICY environments_tenant_isolation
  ON "environments"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY environments_operations_access
  ON "environments"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
