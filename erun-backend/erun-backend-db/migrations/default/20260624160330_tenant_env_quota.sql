-- Create "tenant_quotas" table
CREATE TABLE "tenant_quotas" (
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "max_environments" integer NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("tenant_id"),
  CONSTRAINT "tenant_quotas_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "tenant_quotas_max_environments_check" CHECK (max_environments >= 0)
);
-- Timestamp trigger for the new table (atlas migrate diff omits trigger objects
-- without an atlas login session; appended from the declarative schema;
-- references the existing erun_set_timestamps()).
CREATE TRIGGER tenant_quotas_set_timestamps
  BEFORE INSERT OR UPDATE ON "tenant_quotas"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
-- Grant tenant-owned CRUD on the new table to the application roles (atlas
-- migrate diff omits permissions without an atlas login session; appended from
-- schema/roles.sql). erun_tenant gets RLS-scoped access; erun_operations crosses
-- tenants under its own policies.
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "tenant_quotas"
  TO erun_tenant, erun_operations;
-- Row-level security for the new tenant-owned table (atlas migrate diff omits
-- policies without an atlas login session; appended from
-- schema/rls/tenant_quotas.sql). Two policies mirror the reviews/contexts
-- template: tenant_isolation scopes erun_tenant by erun_current_tenant_id();
-- operations_access lets erun_operations cross tenants.
ALTER TABLE "tenant_quotas" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "tenant_quotas" FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_quotas_tenant_isolation
  ON "tenant_quotas"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY tenant_quotas_operations_access
  ON "tenant_quotas"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
