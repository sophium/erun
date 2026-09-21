-- The per-environment filtered read of the same cursor walk
-- environment_events_tenant_seq_key already serves unfiltered.
CREATE INDEX environment_events_tenant_environment_seq_idx
  ON environment_events (tenant_id, environment_id, environment_event_seq);
