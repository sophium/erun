-- Retention sweep for AI session status rows; see erun-backend-db/AGENTS.md, "AI Sessions".
-- Requires psql variable dry_run (true|false).

SET ROLE erun_operations;

-- ai_sessions: 14-day age or 500-per-(tenant, environment) cap, exited sessions only;
-- a live session is exempt regardless of age.
WITH exited_ranked AS (
  SELECT tenant_id, environment_id, session_id, occurred_at,
         row_number() OVER (PARTITION BY tenant_id, environment_id ORDER BY occurred_at DESC) AS rn
  FROM ai_sessions
  WHERE event = 'exit'
)
SELECT 'ai_sessions' AS table_name, count(*) AS eligible_for_deletion
FROM exited_ranked
WHERE rn > 500 OR occurred_at < now() - interval '14 days';

-- deleted_count equals eligible_count on a real run: the delete repeats this predicate under
-- retention.sh's sweep-wide advisory lock.
WITH exited_ranked AS (
  SELECT tenant_id, environment_id, session_id, occurred_at,
         row_number() OVER (PARTITION BY tenant_id, environment_id ORDER BY occurred_at DESC) AS rn
  FROM ai_sessions
  WHERE event = 'exit'
),
ai_sessions_eligible AS (
  SELECT count(*) AS n FROM exited_ranked
  WHERE rn > 500 OR occurred_at < now() - interval '14 days'
)
INSERT INTO retention_runs (policy_name, table_name, dry_run, eligible_count, deleted_count)
SELECT 'ai_sessions', 'ai_sessions', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM ai_sessions_eligible;

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
