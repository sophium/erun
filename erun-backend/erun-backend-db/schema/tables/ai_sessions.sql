-- One row per (environment, session); each report replaces it, since only the latest
-- turn-boundary event decides busy/idle/awaiting-input.
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
  -- A session row for a deleted environment describes nothing, so it cascades.
  CONSTRAINT ai_sessions_tenant_environment_fkey FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id) ON DELETE CASCADE,
  CONSTRAINT ai_sessions_session_id_check CHECK (length(trim(session_id)) > 0),
  CONSTRAINT ai_sessions_event_check CHECK (event IN ('turn-start', 'tool-use', 'turn-end', 'notify', 'exit'))
);
