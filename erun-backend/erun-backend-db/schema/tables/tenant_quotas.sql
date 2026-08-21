CREATE TABLE tenant_quotas (
  tenant_id UUID PRIMARY KEY DEFAULT erun_current_tenant_id(),
  max_environments INT NOT NULL CHECK (max_environments >= 0),
  -- Per-environment namespace ceiling, not an aggregate tenant budget: every
  -- runtime environment this tenant provisions gets a ResourceQuota/LimitRange
  -- capped at these values (erun-common/kubernetes_resource_quota.go). Defaults
  -- cover the erun-devops chart's own default runtime pod summed across BOTH
  -- its containers (erun-devops + erun-dind, cpu limit 4 + 4, memory limit
  -- 8916Mi + 8916Mi) plus its three default PVCs (2Gi+50Gi+20Gi=72Gi), so a
  -- tenant with no quota row set can still provision a stock runtime
  -- environment (#1061; see eruncommon.MinimumRuntimeNamespaceQuota, the
  -- single source these mirror).
  max_cpu_millicores INT NOT NULL DEFAULT 8000 CHECK (max_cpu_millicores >= 0),
  max_memory_mb INT NOT NULL DEFAULT 17832 CHECK (max_memory_mb >= 0),
  max_storage_gb INT NOT NULL DEFAULT 72 CHECK (max_storage_gb >= 0),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id)
);
