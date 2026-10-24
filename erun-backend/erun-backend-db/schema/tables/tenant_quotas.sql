CREATE TABLE tenant_quotas (
  tenant_id UUID PRIMARY KEY DEFAULT erun_current_tenant_id(),
  max_environments INT NOT NULL CHECK (max_environments >= 0),
  -- Per-environment namespace ceiling, not a tenant budget. Defaults mirror
  -- MinimumRuntimeNamespaceQuota so a stock runtime environment fits.
  max_cpu_millicores INT NOT NULL DEFAULT 8000 CHECK (max_cpu_millicores >= 0),
  max_memory_mb INT NOT NULL DEFAULT 17832 CHECK (max_memory_mb >= 0),
  max_storage_gb INT NOT NULL DEFAULT 72 CHECK (max_storage_gb >= 0),
  -- Tenant-wide budget: admission refuses a create once (env count + 1) * the
  -- per-environment cap exceeds it. Defaults are 10 * the per-environment defaults.
  max_total_cpu_millicores INT NOT NULL DEFAULT 80000 CHECK (max_total_cpu_millicores >= 0),
  max_total_memory_mb INT NOT NULL DEFAULT 178320 CHECK (max_total_memory_mb >= 0),
  max_total_storage_gb INT NOT NULL DEFAULT 720 CHECK (max_total_storage_gb >= 0),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id)
);
