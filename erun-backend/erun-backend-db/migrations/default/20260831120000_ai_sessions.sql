-- Structured AI-session status over the authenticated edge: one row
-- per (environment, session) holding the last turn-boundary event that
-- environment's own AI-tool hooks reported, the authenticated-edge twin of
-- eruncommon.AISessionRecord's local activity-dir file.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/ai_sessions.sql, schema/rls/ai_sessions.sql,
-- and the added trigger in schema/triggers/timestamps.sql.

CREATE TABLE "ai_sessions" (
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "environment_id" uuid NOT NULL,
  "session_id" text NOT NULL,
  "tool" text NULL,
  "event" text NOT NULL,
  "occurred_at" timestamptz NOT NULL,
  "exit_code" integer NULL,
  "exit_reason" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("tenant_id", "environment_id", "session_id"),
  CONSTRAINT "ai_sessions_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "ai_sessions_tenant_environment_fkey" FOREIGN KEY ("tenant_id", "environment_id") REFERENCES "environments" ("tenant_id", "environment_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ai_sessions_session_id_check" CHECK (length(trim(session_id)) > 0),
  CONSTRAINT "ai_sessions_event_check" CHECK (event IN ('turn-start', 'tool-use', 'turn-end', 'notify', 'exit'))
);

CREATE TRIGGER "ai_sessions_set_timestamps"
  BEFORE INSERT OR UPDATE ON "ai_sessions"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

GRANT SELECT, INSERT, UPDATE, REFERENCES
  ON "ai_sessions"
  TO erun_tenant, erun_operations;

ALTER TABLE "ai_sessions" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "ai_sessions" FORCE ROW LEVEL SECURITY;

CREATE POLICY ai_sessions_tenant_isolation
  ON "ai_sessions"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());

CREATE POLICY ai_sessions_operations_access
  ON "ai_sessions"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
