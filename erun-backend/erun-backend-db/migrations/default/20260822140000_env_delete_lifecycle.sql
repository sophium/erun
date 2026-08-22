-- A blocked namespace teardown must not leave the environment row claiming
-- `running`: add the two states a delete attempt can actually land in
-- (`deleting`, in flight; `deletion-blocked`, attempted and not torn down)
-- plus delete_error to carry the blocker's own reason, mirroring
-- provision_error/expose_error.
-- Hand-written column add + constraint replace (atlas migrate diff is
-- login-gated on the RLS functions in the source schema); no new table/RLS,
-- so the existing environments row-level security already covers this column.
ALTER TABLE "environments" ADD COLUMN "delete_error" text NULL;

ALTER TABLE "environments" DROP CONSTRAINT "environments_status_check";
ALTER TABLE "environments" ADD CONSTRAINT "environments_status_check" CHECK (status IN ('registered', 'provisioning', 'running', 'failed', 'deleting', 'deletion-blocked'));
