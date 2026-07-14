-- Add the provisioning-status columns to "environments" (#605 Slice A): an env
-- row is `registered` when created, then the server-side deploy executor moves
-- it `provisioning` -> `running`/`failed`. Mirrors the contexts status pattern.
-- Hand-written column add (atlas migrate diff is login-gated on the RLS
-- functions in the source schema); no new table/RLS, so the existing
-- environments row-level security already covers these columns.
ALTER TABLE "environments" ADD COLUMN "status" text NOT NULL DEFAULT 'registered';
ALTER TABLE "environments" ADD COLUMN "provision_error" text NULL;
ALTER TABLE "environments" ADD CONSTRAINT "environments_status_check" CHECK (status IN ('registered', 'provisioning', 'running', 'failed'));
