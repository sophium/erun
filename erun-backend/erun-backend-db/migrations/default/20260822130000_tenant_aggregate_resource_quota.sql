-- Aggregate per-tenant CPU/memory/storage ceiling (#1113), distinct from the
-- existing per-environment ceiling on tenant_quotas: every runtime
-- environment gets the same per-environment cap, so admission projects
-- (existing runtime environment count + 1) * the per-environment cap against
-- this budget and refuses a create that would exceed it.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/tenant_quotas.sql.

ALTER TABLE "tenant_quotas"
  ADD COLUMN "max_total_cpu_millicores" integer NOT NULL DEFAULT 80000,
  ADD COLUMN "max_total_memory_mb" integer NOT NULL DEFAULT 178320,
  ADD COLUMN "max_total_storage_gb" integer NOT NULL DEFAULT 720;

ALTER TABLE "tenant_quotas"
  ADD CONSTRAINT "tenant_quotas_max_total_cpu_millicores_check" CHECK (max_total_cpu_millicores >= 0),
  ADD CONSTRAINT "tenant_quotas_max_total_memory_mb_check" CHECK (max_total_memory_mb >= 0),
  ADD CONSTRAINT "tenant_quotas_max_total_storage_gb_check" CHECK (max_total_storage_gb >= 0);
