-- Add "kind" to "builds" (schema/tables/builds.sql). RECORDED covers every
-- existing row (a client-reported build or a release's own build), each of
-- which always names the version it produced; GATE is the merge queue's own
-- prospective-merge build, which publishes nothing and so mints no version.
ALTER TABLE "builds" ADD COLUMN "kind" text NOT NULL DEFAULT 'RECORDED';
ALTER TABLE "builds" ADD CONSTRAINT "builds_kind_check" CHECK (kind IN ('RECORDED', 'GATE'));

-- "version" stops being universally required: a GATE build never has one.
-- NULL is checked explicitly for the same reason failure_detail is below: a
-- bare length(trim(version)) > 0 evaluates to NULL, not FALSE, for a NULL
-- version, and PostgreSQL accepts a CHECK whose result is NULL.
ALTER TABLE "builds" DROP CONSTRAINT "builds_version_check";
ALTER TABLE "builds" ALTER COLUMN "version" DROP NOT NULL;
ALTER TABLE "builds" ADD CONSTRAINT "builds_version_check"
  CHECK (kind = 'GATE' OR (version IS NOT NULL AND length(trim(version)) > 0));

-- "failure_detail" carries a failed GATE build's own reason (the #1021
-- precedent: a recorded failure names something an operator can act on, not
-- just that a Job exited). A caller-reported RECORDED failure has nowhere to
-- put a reason here — that lives wherever the caller's own CI recorded it.
ALTER TABLE "builds" ADD COLUMN "failure_detail" text;
-- NULL failure_detail must be checked explicitly: a bare
-- `length(trim(failure_detail)) > 0` evaluates to NULL (not FALSE) for a NULL
-- failure_detail, and PostgreSQL accepts a CHECK whose result is NULL, so that
-- form would silently accept a failed GATE build with no reason at all.
ALTER TABLE "builds" ADD CONSTRAINT "builds_failure_detail_check"
  CHECK (successful OR kind <> 'GATE' OR (failure_detail IS NOT NULL AND length(trim(failure_detail)) > 0));
