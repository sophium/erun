-- The default tenant_quotas caps were sized for one runtime pod container;
-- a ResourceQuota counts both erun-devops and erun-dind, so the stock runtime
-- pod could never admit itself under the old defaults (#1061). Raise the
-- column defaults to match eruncommon.MinimumRuntimeNamespaceQuota, the
-- single source erun-backend-api's repository.DefaultMax* now derives from.
--
-- Hand-written (atlas migrate diff is login-gated on the RLS functions in the
-- source schema); mirrors schema/tables/tenant_quotas.sql.

ALTER TABLE "tenant_quotas"
  ALTER COLUMN "max_cpu_millicores" SET DEFAULT 8000,
  ALTER COLUMN "max_memory_mb" SET DEFAULT 17832,
  ALTER COLUMN "max_storage_gb" SET DEFAULT 72;
