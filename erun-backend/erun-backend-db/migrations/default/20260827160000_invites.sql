-- Create "invites" table (schema/tables/invites.sql): registration is
-- invite-only. An invite is a server-side record (revocable, listable,
-- single-use), not a self-contained signed token — issuer is captured from
-- the inviter's own authenticated issuer at creation time so the accept
-- flow, which runs with no caller identity of its own, links the new user's
-- external identity to the same IdP the inviter reached the platform through.
CREATE TABLE "invites" (
  "invite_id" uuid NOT NULL DEFAULT uuidv7(),
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "created_by_user_id" uuid NOT NULL DEFAULT erun_current_user_id(),
  "issuer" text NOT NULL,
  "token" text NOT NULL,
  "email" text NULL,
  "expires_at" timestamptz NOT NULL,
  "consumed_at" timestamptz NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("invite_id"),
  CONSTRAINT "invites_issuer_check" CHECK (length(trim("issuer")) > 0),
  CONSTRAINT "invites_token_check" CHECK (length(trim("token")) > 0),
  CONSTRAINT "invites_token_key" UNIQUE ("token"),
  CONSTRAINT "invites_tenant_invite_key" UNIQUE ("tenant_id", "invite_id"),
  CONSTRAINT "invites_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  -- Unlike reviews.author_user_id, an invite's creator is not necessarily a
  -- member of the invite's own target tenant (an OPERATIONS caller may mint
  -- an invite for a different tenant, #1483 item 4), so this references the
  -- global users.user_id primary key rather than the tenant-scoped
  -- (tenant_id, user_id) composite key reviews.sql uses.
  CONSTRAINT "invites_created_by_user_id_fkey" FOREIGN KEY ("created_by_user_id") REFERENCES "users" ("user_id") ON UPDATE NO ACTION ON DELETE NO ACTION
);

-- Outstanding-invites listing index (schema/indexes/invites.sql): partial,
-- since only unconsumed invites are what the console lists.
CREATE INDEX "invites_tenant_outstanding_idx" ON "invites" ("tenant_id", "created_at") WHERE ("consumed_at" IS NULL);

-- Timestamp trigger for the new table (atlas migrate diff omits trigger
-- objects without an atlas login session; appended from the declarative
-- schema; references the existing erun_set_timestamps()).
CREATE TRIGGER invites_set_timestamps
  BEFORE INSERT OR UPDATE ON "invites"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

-- Grant tenant-owned CRUD on the new table to the application roles (atlas
-- migrate diff omits permissions without an atlas login session; appended
-- from schema/roles.sql). erun_tenant gets RLS-scoped access; erun_operations
-- crosses tenants under its own policies, which is also what the
-- unauthenticated accept endpoint's token lookup runs as (see
-- repository.TxManager.WithinSystemTx).
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "invites"
  TO erun_tenant, erun_operations;

-- Row-level security for the new tenant-owned table (atlas migrate diff omits
-- policies without an atlas login session; appended from
-- schema/rls/invites.sql). Two policies mirror the reviews template:
-- tenant_isolation scopes erun_tenant by erun_current_tenant_id();
-- operations_access lets erun_operations cross tenants.
ALTER TABLE "invites" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "invites" FORCE ROW LEVEL SECURITY;
CREATE POLICY invites_tenant_isolation
  ON "invites"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY invites_operations_access
  ON "invites"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
