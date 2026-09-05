-- The delete reconciler re-attempted a blocked teardown forever: no attempt
-- counter, no backoff, and no terminal state for "this cannot be torn down
-- without intervention" (#1166). delete_attempts is what bounds it -- the
-- claim increments it, so the reconciler can back off per attempt and stop
-- re-attempting past a cap, instead of clearing and re-recording the same
-- blocker on a five-minute timer indefinitely.
-- Hand-written column add (atlas migrate diff is login-gated on the RLS
-- functions in the source schema); no new table/RLS, so the existing
-- environments row-level security already covers this column.
ALTER TABLE "environments" ADD COLUMN "delete_attempts" integer NOT NULL DEFAULT 0;
