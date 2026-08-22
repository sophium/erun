-- Placement capacity for multi-cluster environment provisioning (#1112): each
-- context (a tenant's own bootstrapped cluster) names how many environments it
-- may host, so the platform's context inventory is entirely data-driven -- no
-- code-level cluster list.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/contexts.sql.

ALTER TABLE "contexts"
  ADD COLUMN "max_environments" integer NOT NULL DEFAULT 20;

ALTER TABLE "contexts"
  ADD CONSTRAINT "contexts_max_environments_check" CHECK (max_environments >= 0);
