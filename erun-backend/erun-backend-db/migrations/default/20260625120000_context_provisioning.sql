-- Async live provisioning of cloud contexts (issues #605/#676). Adds the
-- provisioning lifecycle to contexts, the tenant's BYO-cloud credentials, and
-- server-side custody of the k3s admin token produced by the bootstrap.

-- contexts gains a provisioning lifecycle. status tracks the durable
-- (DBOS-driven) bootstrap; provision_error carries the failure reason the
-- console surfaces. Default 'provisioning' so a freshly-created context starts
-- in flight; the executor transitions it to running/failed.
ALTER TABLE "contexts" ADD COLUMN "status" text NOT NULL DEFAULT 'provisioning';
ALTER TABLE "contexts" ADD COLUMN "provision_error" text NULL;
ALTER TABLE "contexts" ADD CONSTRAINT "contexts_status_check" CHECK (status IN ('provisioning', 'running', 'failed'));

-- Create "cloud_provider_aliases" table: the tenant's BYO-cloud credentials
-- (encrypted at rest), resolved when provisioning a context. Tenant-owned.
CREATE TABLE "cloud_provider_aliases" (
  "cloud_provider_alias_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "alias" text NOT NULL,
  "provider" text NOT NULL,
  "credentials_encrypted" bytea NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("cloud_provider_alias_id"),
  CONSTRAINT "cloud_provider_aliases_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "cloud_provider_aliases_provider_check" CHECK (provider IN ('aws')),
  CONSTRAINT "cloud_provider_aliases_tenant_alias_key" UNIQUE ("tenant_id", "alias")
);

-- Create "context_credentials" table: the k3s admin token (encrypted at rest)
-- custodied server-side after a successful bootstrap. Kept out of the contexts
-- read model, which deliberately excludes the secret; one row per context.
CREATE TABLE "context_credentials" (
  "context_credential_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "context_id" uuid NOT NULL,
  "k3s_admin_token_encrypted" bytea NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("context_credential_id"),
  CONSTRAINT "context_credentials_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "context_credentials_context_fkey" FOREIGN KEY ("tenant_id", "context_id") REFERENCES "contexts" ("tenant_id", "context_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "context_credentials_tenant_context_key" UNIQUE ("tenant_id", "context_id")
);

-- Timestamp triggers for the new tables (atlas migrate diff omits trigger
-- objects without an atlas login session; appended from the declarative schema;
-- reference the existing erun_set_timestamps()).
CREATE TRIGGER cloud_provider_aliases_set_timestamps
  BEFORE INSERT OR UPDATE ON "cloud_provider_aliases"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
CREATE TRIGGER context_credentials_set_timestamps
  BEFORE INSERT OR UPDATE ON "context_credentials"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

-- Grant tenant-owned CRUD on the new tables to the application roles (atlas
-- migrate diff omits permissions without an atlas login session; appended from
-- schema/roles.sql). erun_tenant gets RLS-scoped access; erun_operations crosses
-- tenants under its own policies.
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "cloud_provider_aliases"
  TO erun_tenant, erun_operations;
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "context_credentials"
  TO erun_tenant, erun_operations;

-- Row-level security for the new tenant-owned tables (atlas migrate diff omits
-- policies without an atlas login session; appended from schema/rls/*.sql). Two
-- policies mirror the contexts/tenant_quotas template: tenant_isolation scopes
-- erun_tenant by erun_current_tenant_id(); operations_access lets erun_operations
-- cross tenants.
ALTER TABLE "cloud_provider_aliases" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "cloud_provider_aliases" FORCE ROW LEVEL SECURITY;
CREATE POLICY cloud_provider_aliases_tenant_isolation
  ON "cloud_provider_aliases"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY cloud_provider_aliases_operations_access
  ON "cloud_provider_aliases"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
ALTER TABLE "context_credentials" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "context_credentials" FORCE ROW LEVEL SECURITY;
CREATE POLICY context_credentials_tenant_isolation
  ON "context_credentials"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY context_credentials_operations_access
  ON "context_credentials"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
