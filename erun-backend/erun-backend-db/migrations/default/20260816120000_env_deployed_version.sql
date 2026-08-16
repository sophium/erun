-- Record which runtime version an environment is actually running, distinct
-- from `runtime_version` (the operator's declared pin). The deploy executor
-- writes it when the deploy lands, so an env can always name what it is running
-- -- which is exactly what recovery needs after a later deploy of a different
-- version fails.
-- Hand-written column add (atlas migrate diff is login-gated on the RLS
-- functions in the source schema); no new table/RLS, so the existing
-- environments row-level security already covers this column.
ALTER TABLE "environments" ADD COLUMN "deployed_version" text NULL;
