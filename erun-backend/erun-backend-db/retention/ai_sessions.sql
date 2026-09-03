-- Retention sweep for AI session status rows. See erun-backend-db/AGENTS.md,
-- "AI Sessions" for the design these bounds implement.
--
-- Required variable: dry_run (true|false). Run via:
--   psql -v ON_ERROR_STOP=1 -v dry_run=true|false -f ai_sessions.sql "$ERUN_DATABASE_URL"

SET ROLE erun_operations;

-- ai_sessions: 14-day age bound on occurred_at or 500-most-recent-per-
-- (tenant, environment) count cap, either bound prunes, applied only to
-- rows whose last reported event is 'exit'. A session still turn-start/
-- tool-use/turn-end/notify is live resolved status and is exempt regardless
-- of age -- the same "don't delete a live row" shape as comments' OPEN-thread
-- exemption. Nothing references ai_sessions, so no guard is needed beyond
-- the bound itself.
WITH exited_ranked AS (
  SELECT tenant_id, environment_id, session_id, occurred_at,
         row_number() OVER (PARTITION BY tenant_id, environment_id ORDER BY occurred_at DESC) AS rn
  FROM ai_sessions
  WHERE event = 'exit'
)
SELECT 'ai_sessions' AS table_name, count(*) AS eligible_for_deletion
FROM exited_ranked
WHERE rn > 500 OR occurred_at < now() - interval '14 days';

\if :dry_run
\else
BEGIN;

WITH exited_ranked AS (
  SELECT tenant_id, environment_id, session_id, occurred_at,
         row_number() OVER (PARTITION BY tenant_id, environment_id ORDER BY occurred_at DESC) AS rn
  FROM ai_sessions
  WHERE event = 'exit'
),
sessions_to_delete AS (
  SELECT tenant_id, environment_id, session_id FROM exited_ranked
  WHERE rn > 500 OR occurred_at < now() - interval '14 days'
)
DELETE FROM ai_sessions
WHERE (tenant_id, environment_id, session_id) IN (SELECT tenant_id, environment_id, session_id FROM sessions_to_delete);

COMMIT;
\endif
