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
-- Modify "tenant_issuers" table
ALTER TABLE "tenant_issuers" DROP CONSTRAINT "tenant_issuers_pkey", ADD COLUMN "org_field_value" text NULL, ADD CONSTRAINT "tenant_issuers_issuer_org_key" UNIQUE NULLS NOT DISTINCT ("issuer", "org_field_value"), ADD CONSTRAINT "tenant_issuers_issuer_fkey" FOREIGN KEY ("issuer") REFERENCES "issuers" ("issuer") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Timestamp trigger for the new issuers table (atlas migrate diff omits
-- trigger objects without an atlas login session; appended from the
-- declarative schema; references the existing erun_set_timestamps()).
CREATE TRIGGER issuers_set_timestamps
  BEFORE INSERT OR UPDATE ON "issuers"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
