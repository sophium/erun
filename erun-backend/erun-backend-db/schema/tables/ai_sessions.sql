-- One row per (environment, session): the last turn-boundary event that
-- environment's own AI-tool hooks reported, the authenticated-edge twin of
-- eruncommon.AISessionRecord (erun-common/ai_session_status.go). A later
-- report replaces the row outright -- only the most recent event decides the
-- resolved busy/idle/awaiting-input state.
CREATE TABLE ai_sessions (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  environment_id UUID NOT NULL,
  session_id TEXT NOT NULL,
  tool TEXT,
  event TEXT NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  exit_code INT,
  exit_reason TEXT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, environment_id, session_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  -- Cascades with the environment: a session-status row for a deleted
  -- environment describes nothing real, unlike usage_events' own
  -- environment_id FK, which is deliberately SET NULL to keep an append-only
  -- metering trail alive past the row it described.
  CONSTRAINT ai_sessions_tenant_environment_fkey FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id) ON DELETE CASCADE,
  CONSTRAINT ai_sessions_session_id_check CHECK (length(trim(session_id)) > 0),
  CONSTRAINT ai_sessions_event_check CHECK (event IN ('turn-start', 'tool-use', 'turn-end', 'notify', 'exit'))
);
