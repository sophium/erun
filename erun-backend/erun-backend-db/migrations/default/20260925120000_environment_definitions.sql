-- The environment definition store: the portable subset of an
-- environment's config, uploaded from the machine that authored it, so a second
-- machine can pull the same environment down.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/environment_definitions.sql,
-- schema/indexes/environment_definitions.sql, schema/triggers/timestamps.sql,
-- schema/rls/environment_definitions.sql, and schema/roles.sql.

CREATE TABLE "environment_definitions" (
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "environment_id" uuid NOT NULL,
  "revision" integer NOT NULL DEFAULT 1,
  "definition" jsonb NOT NULL,
  "written_by_user_id" uuid NOT NULL DEFAULT erun_current_user_id(),
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("tenant_id", "environment_id"),
  CONSTRAINT "environment_definitions_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "environment_definitions_tenant_environment_fkey" FOREIGN KEY ("tenant_id", "environment_id") REFERENCES "environments" ("tenant_id", "environment_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "environment_definitions_revision_check" CHECK ("revision" > 0),
  CONSTRAINT "environment_definitions_definition_object_check" CHECK (jsonb_typeof("definition") = 'object')
);

CREATE INDEX "environment_definitions_tenant_updated_at_idx" ON "environment_definitions" ("tenant_id", "updated_at" DESC);

CREATE TRIGGER "environment_definitions_set_timestamps"
  BEFORE INSERT OR UPDATE ON "environment_definitions"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "environment_definitions"
  TO erun_tenant, erun_operations;

ALTER TABLE "environment_definitions" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "environment_definitions" FORCE ROW LEVEL SECURITY;

CREATE POLICY environment_definitions_tenant_isolation
  ON "environment_definitions"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY environment_definitions_operations_access
  ON "environment_definitions"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
