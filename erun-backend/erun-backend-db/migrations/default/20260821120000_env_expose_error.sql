-- Record why a deploy Job's chained exposure did not succeed, distinct from
-- provision_error: exposure (DNS + Ingress) is best-effort, so a healthy
-- deployed environment stays `running` and names its own unexposed state
-- here instead of the whole environment being recorded `failed` for a step
-- that left the deployed workload untouched (#1086).
-- Hand-written column add (atlas migrate diff is login-gated on the RLS
-- functions in the source schema); no new table/RLS, so the existing
-- environments row-level security already covers this column.
ALTER TABLE "environments" ADD COLUMN "expose_error" text NULL;
