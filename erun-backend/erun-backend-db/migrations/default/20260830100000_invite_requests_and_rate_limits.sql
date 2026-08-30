-- Create "invite_requests" table (schema/tables/invite_requests.sql): a
-- verified-issuer/subject request to join an existing tenant or have a new
-- one registered, the request-and-approve queue that replaces the two
-- CLI/console-only onboarding steps.
CREATE TABLE "invite_requests" (
  "invite_request_id" uuid NOT NULL DEFAULT uuidv7(),
  "issuer" text NOT NULL,
  "subject" text NOT NULL,
  "email" text NULL,
  "display_name" text NULL,
  "kind" text NOT NULL,
  "tenant_name" text NOT NULL,
  "environment_name" text NULL,
  "note" text NULL,
  "status" text NOT NULL DEFAULT 'PENDING',
  "decided_by_user_id" uuid NULL,
  "decline_reason" text NULL,
  "minted_invite_id" uuid NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("invite_request_id"),
  CONSTRAINT "invite_requests_issuer_check" CHECK (length(trim("issuer")) > 0),
  CONSTRAINT "invite_requests_subject_check" CHECK (length(trim("subject")) > 0),
  CONSTRAINT "invite_requests_tenant_name_check" CHECK (length(trim("tenant_name")) > 0),
  CONSTRAINT "invite_requests_kind_check" CHECK ("kind" IN ('JOIN_TENANT', 'CREATE_TENANT')),
  CONSTRAINT "invite_requests_status_check" CHECK ("status" IN ('PENDING', 'APPROVED', 'DECLINED')),
  CONSTRAINT "invite_requests_decline_reason_check" CHECK ("status" <> 'DECLINED' OR ("decline_reason" IS NOT NULL AND length(trim("decline_reason")) > 0)),
  CONSTRAINT "invite_requests_decided_by_user_id_fkey" FOREIGN KEY ("decided_by_user_id") REFERENCES "users" ("user_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "invite_requests_minted_invite_id_fkey" FOREIGN KEY ("minted_invite_id") REFERENCES "invites" ("invite_id") ON UPDATE NO ACTION ON DELETE NO ACTION
);

-- Create "platform_rate_limits" table (schema/tables/platform_rate_limits.sql):
-- the singleton platform-scoped request-rate-limit configuration.
CREATE TABLE "platform_rate_limits" (
  "singleton" boolean NOT NULL DEFAULT true,
  "invite_request_window_seconds" integer NOT NULL DEFAULT 60,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("singleton"),
  CONSTRAINT "platform_rate_limits_singleton_check" CHECK ("singleton"),
  CONSTRAINT "platform_rate_limits_invite_request_window_seconds_check" CHECK ("invite_request_window_seconds" > 0)
);

-- Abuse-bound indexes (schema/indexes/invite_requests.sql): the partial
-- unique index is the schema-level enforcement of "one pending request per
-- (issuer, subject)"; the second supports the operator queue's listing.
CREATE UNIQUE INDEX "invite_requests_pending_issuer_subject_idx" ON "invite_requests" ("issuer", "subject") WHERE ("status" = 'PENDING');
CREATE INDEX "invite_requests_status_tenant_name_idx" ON "invite_requests" ("status", "tenant_name", "created_at");

-- Timestamp triggers for the two new tables (atlas migrate diff omits
-- trigger objects without an atlas login session; appended from the
-- declarative schema/triggers/timestamps.sql, which already lists them).
CREATE TRIGGER invite_requests_set_timestamps
  BEFORE INSERT OR UPDATE ON "invite_requests"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER platform_rate_limits_set_timestamps
  BEFORE INSERT OR UPDATE ON "platform_rate_limits"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

-- Grants (atlas migrate diff omits permissions without an atlas login
-- session; appended from schema/roles.sql). invite_requests carries no RLS —
-- like tenants/tenant_issuers, it is a root resolution table read/written by
-- a caller with no tenant yet, or a tenant admin/operator acting on a
-- request's own free-text tenant_name rather than a tenant-scoped row — so
-- application code, not RLS, scopes which rows a non-operations caller may
-- act on (see internal/repository/invite_requests.go). platform_rate_limits
-- is read-only for erun_tenant (any tenant reflects the current window) and
-- read/write for erun_operations (the only caller allowed to change it).
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES ON "invite_requests" TO erun_tenant, erun_operations;
GRANT SELECT ON "platform_rate_limits" TO erun_tenant;
GRANT SELECT, INSERT, UPDATE, REFERENCES ON "platform_rate_limits" TO erun_operations;
