-- Retention sweep for invites and invite_requests; see erun-backend-db/AGENTS.md,
-- "Invites And Invite Requests". Requires psql variable dry_run (true|false).

SET ROLE erun_operations;

-- invite_requests: 180-day age (since decision) or 10,000 cap, decided rows only. The cap is
-- platform-wide because the table has no tenant_id.
WITH decided_ranked AS (
  SELECT invite_request_id, updated_at,
         row_number() OVER (ORDER BY updated_at DESC) AS rn
  FROM invite_requests
  WHERE status IN ('APPROVED', 'DECLINED')
)
SELECT 'invite_requests' AS table_name, count(*) AS eligible_for_deletion
FROM decided_ranked
WHERE rn > 10000 OR updated_at < now() - interval '180 days';

-- invites: consumed and expired-unconsumed invites rank separately so one cannot evict the
-- other's history; live invites are exempt. Rows invite_requests.minted_invite_id still
-- references are skipped (RESTRICT FK).
WITH consumed_ranked AS (
  SELECT invite_id, tenant_id, consumed_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY consumed_at DESC) AS rn
  FROM invites
  WHERE consumed_at IS NOT NULL
),
expired_unconsumed_ranked AS (
  SELECT invite_id, tenant_id, expires_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY expires_at DESC) AS rn
  FROM invites
  WHERE consumed_at IS NULL AND expires_at < now()
),
invites_eligible AS (
  SELECT invite_id FROM consumed_ranked
  WHERE rn > 10000 OR consumed_at < now() - interval '365 days'
  UNION
  SELECT invite_id FROM expired_unconsumed_ranked
  WHERE rn > 10000 OR expires_at < now() - interval '30 days'
)
SELECT 'invites' AS table_name, count(*) AS eligible_for_deletion
FROM invites_eligible i
WHERE NOT EXISTS (SELECT 1 FROM invite_requests ir WHERE ir.minted_invite_id = i.invite_id);

-- deleted_count equals eligible_count on a real run: the delete repeats this predicate under
-- retention.sh's sweep-wide advisory lock.
WITH decided_ranked AS (
  SELECT invite_request_id, updated_at,
         row_number() OVER (ORDER BY updated_at DESC) AS rn
  FROM invite_requests
  WHERE status IN ('APPROVED', 'DECLINED')
),
invite_requests_eligible AS (
  SELECT count(*) AS n FROM decided_ranked
  WHERE rn > 10000 OR updated_at < now() - interval '180 days'
),
consumed_ranked AS (
  SELECT invite_id, tenant_id, consumed_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY consumed_at DESC) AS rn
  FROM invites
  WHERE consumed_at IS NOT NULL
),
expired_unconsumed_ranked AS (
  SELECT invite_id, tenant_id, expires_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY expires_at DESC) AS rn
  FROM invites
  WHERE consumed_at IS NULL AND expires_at < now()
),
invites_eligible2 AS (
  SELECT invite_id FROM consumed_ranked
  WHERE rn > 10000 OR consumed_at < now() - interval '365 days'
  UNION
  SELECT invite_id FROM expired_unconsumed_ranked
  WHERE rn > 10000 OR expires_at < now() - interval '30 days'
),
invites_eligible_count AS (
  SELECT count(*) AS n
  FROM invites_eligible2 i
  WHERE NOT EXISTS (SELECT 1 FROM invite_requests ir WHERE ir.minted_invite_id = i.invite_id)
)
INSERT INTO retention_runs (policy_name, table_name, dry_run, eligible_count, deleted_count)
SELECT 'invites_invite_requests', 'invite_requests', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM invite_requests_eligible
UNION ALL
SELECT 'invites_invite_requests', 'invites', :dry_run, n, CASE WHEN :dry_run THEN 0 ELSE n END FROM invites_eligible_count;

\if :dry_run
\else
BEGIN;

WITH decided_ranked AS (
  SELECT invite_request_id, updated_at,
         row_number() OVER (ORDER BY updated_at DESC) AS rn
  FROM invite_requests
  WHERE status IN ('APPROVED', 'DECLINED')
),
requests_to_delete AS (
  SELECT invite_request_id FROM decided_ranked
  WHERE rn > 10000 OR updated_at < now() - interval '180 days'
)
DELETE FROM invite_requests WHERE invite_request_id IN (SELECT invite_request_id FROM requests_to_delete);

WITH consumed_ranked AS (
  SELECT invite_id, tenant_id, consumed_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY consumed_at DESC) AS rn
  FROM invites
  WHERE consumed_at IS NOT NULL
),
expired_unconsumed_ranked AS (
  SELECT invite_id, tenant_id, expires_at,
         row_number() OVER (PARTITION BY tenant_id ORDER BY expires_at DESC) AS rn
  FROM invites
  WHERE consumed_at IS NULL AND expires_at < now()
),
invites_to_delete AS (
  SELECT invite_id FROM consumed_ranked
  WHERE rn > 10000 OR consumed_at < now() - interval '365 days'
  UNION
  SELECT invite_id FROM expired_unconsumed_ranked
  WHERE rn > 10000 OR expires_at < now() - interval '30 days'
)
DELETE FROM invites
WHERE invite_id IN (
  SELECT invite_id FROM invites_to_delete i
  WHERE NOT EXISTS (SELECT 1 FROM invite_requests ir WHERE ir.minted_invite_id = i.invite_id)
);

COMMIT;
\endif
