-- A job can now be recorded before its work starts (schema/tables/jobs.sql).
--
-- Until this, RUNNING was the only open status: a job was born running
-- (started_at defaults to now(), and Claim defaults an empty status to
-- RUNNING), so a unit of planned work had no way to sit in the queue without
-- claiming to be underway. PLANNED is that not-yet-started rung -- a triage or
-- plan job recorded against its issue, flipped to RUNNING when coding begins.
--
-- jobs_ended_at_check moves with it. It read
-- ((status = 'RUNNING') = (ended_at IS NULL)), which forces every non-RUNNING
-- row to carry an ended_at; a PLANNED row under that rule would have to look
-- finished to be storable, which is the opposite of what it means. The
-- invariant it is actually protecting is unchanged -- a job that has stopped
-- is never left without the time it stopped at -- so the open set grows by one
-- member rather than the rule being dropped.

ALTER TABLE "jobs" DROP CONSTRAINT "jobs_status_check";

ALTER TABLE "jobs" ADD CONSTRAINT "jobs_status_check"
  CHECK ("status" IN ('PLANNED', 'RUNNING', 'SUCCEEDED', 'FAILED', 'ABANDONED', 'SUPERSEDED'));

ALTER TABLE "jobs" DROP CONSTRAINT "jobs_ended_at_check";

ALTER TABLE "jobs" ADD CONSTRAINT "jobs_ended_at_check"
  CHECK (("status" IN ('PLANNED', 'RUNNING')) = ("ended_at" IS NULL));
