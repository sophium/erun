-- Retention sweep for invites and invite_requests. See
-- erun-backend-db/AGENTS.md, "Invites And Invite Requests" for the design
-- these bounds implement.
--
-- Required variable: dry_run (true|false). Run via:
--   psql -v ON_ERROR_STOP=1 -v dry_run=true|false -f invites_invite_requests.sql "$ERUN_DATABASE_URL"

SET ROLE erun_operations;

-- invite_requests: 180-day age bound (on updated_at, i.e. when the request
-- was decided) or a 10,000-row platform-wide count cap, applied only to
-- APPROVED/DECLINED requests -- a PENDING request is exempt regardless of
-- age, the same "don't delete a live row" shape as comments' OPEN-thread
-- exemption. invite_requests has no tenant_id and no RLS (the submitter
-- isn't authenticated into any tenant yet), so the cap is platform-wide, not
-- per-tenant, and nothing references invite_requests, so no guard is needed
-- beyond the bound itself.
WITH decided_ranked AS (
  SELECT invite_request_id, updated_at,
         row_number() OVER (ORDER BY updated_at DESC) AS rn
  FROM invite_requests
  WHERE status IN ('APPROVED', 'DECLINED')
)
SELECT 'invite_requests' AS table_name, count(*) AS eligible_for_deletion
FROM decided_ranked
WHERE rn > 10000 OR updated_at < now() - interval '180 days';

-- invites: two terminal populations, each bounded by its own age window but
-- sharing the same 10,000-most-recent-per-tenant count cap (one table, one
-- cap value, ranked separately per population so a flood of one kind cannot
-- evict the other's history). A live invite (not yet consumed, not yet
-- expired) is exempt regardless of age. Before deleting either population,
-- guard against invite_requests.minted_invite_id still pointing at the row
-- -- deleting the referenced invite first would violate that RESTRICT FK.
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
