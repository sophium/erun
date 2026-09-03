-- Records the outcome of one retention policy file's sweep of one table,
-- across every tenant -- not tenant-owned itself, since a sweep runs once
-- for the whole platform. Lets an operator see that a scheduled retention
-- run deleted rows and how many.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/retention_runs.sql,
-- schema/indexes/retention_runs.sql, and schema/roles.sql.

CREATE TABLE "retention_runs" (
  "retention_run_id" uuid NOT NULL DEFAULT uuidv7(),
  "policy_name" text NOT NULL,
  "table_name" text NOT NULL,
  "dry_run" boolean NOT NULL,
  "eligible_count" bigint NOT NULL,
  "deleted_count" bigint NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("retention_run_id"),
  CONSTRAINT "retention_runs_policy_name_check" CHECK (length(trim("policy_name")) > 0),
  CONSTRAINT "retention_runs_table_name_check" CHECK (length(trim("table_name")) > 0),
  CONSTRAINT "retention_runs_eligible_count_check" CHECK ("eligible_count" >= 0),
  CONSTRAINT "retention_runs_deleted_count_check" CHECK ("deleted_count" >= 0 AND "deleted_count" <= "eligible_count"),
  CONSTRAINT "retention_runs_dry_run_deleted_count_check" CHECK (NOT "dry_run" OR "deleted_count" = 0)
);

CREATE INDEX "retention_runs_created_at_idx" ON "retention_runs" ("created_at" DESC);

GRANT SELECT, INSERT ON "retention_runs" TO erun_operations;
