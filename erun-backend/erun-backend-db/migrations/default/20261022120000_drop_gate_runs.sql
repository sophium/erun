-- Drop gate_runs. A merge is now landed against an ordinary RECORDED build of
-- the merge commit recorded on its review, so nothing writes or reads a gate
-- run any more. Dropping the table also drops its indexes, its timestamp
-- trigger, its RLS policies and the role grants on it.
--
-- Destructive: every tenant's gate-run history is deleted. The review
-- retention sweep's gate_runs guards were removed in the same release, so the
-- sweep never references the table after this runs.
DROP TABLE "gate_runs";
