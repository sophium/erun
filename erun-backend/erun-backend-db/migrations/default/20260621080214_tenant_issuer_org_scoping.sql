-- Modify "audit_events" table
ALTER TABLE "audit_events" ADD COLUMN "external_org_id" text NULL;
-- Create "issuers" table
CREATE TABLE "issuers" (
  "issuer" text NOT NULL,
  "org_field_key" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("issuer")
);
-- Timestamp trigger for the new issuers table (atlas migrate diff omits
-- trigger objects without an atlas login session; appended from the
-- declarative schema; references the existing erun_set_timestamps()).
-- Created before the backfill below so backfilled issuer rows receive
-- created_at/updated_at.
CREATE TRIGGER issuers_set_timestamps
  BEFORE INSERT OR UPDATE ON "issuers"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
-- Backfill the issuers registry from existing tenant_issuers rows so the
-- foreign key added below is satisfiable on an already-populated database:
-- every distinct issuer already mapped to a tenant must first exist in
-- issuers. New deploys have an empty tenant_issuers, so this is a no-op there.
INSERT INTO "issuers" ("issuer")
SELECT DISTINCT "issuer" FROM "tenant_issuers"
ON CONFLICT ("issuer") DO NOTHING;
-- Modify "tenant_issuers" table
ALTER TABLE "tenant_issuers" DROP CONSTRAINT "tenant_issuers_pkey", ADD COLUMN "org_field_value" text NULL, ADD CONSTRAINT "tenant_issuers_issuer_org_key" UNIQUE NULLS NOT DISTINCT ("issuer", "org_field_value"), ADD CONSTRAINT "tenant_issuers_issuer_fkey" FOREIGN KEY ("issuer") REFERENCES "issuers" ("issuer") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Grant access on the new issuers registry, mirroring tenants/tenant_issuers:
-- erun_operations bootstraps and manages issuer rows; erun_tenant reads them
-- during (iss, org) -> tenant resolution. Without these, first-identity
-- bootstrap fails with permission denied on a brand-new hosted deploy.
GRANT SELECT ON "issuers" TO erun_tenant;
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES ON "issuers" TO erun_operations;
