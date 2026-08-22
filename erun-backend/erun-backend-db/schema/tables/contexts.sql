CREATE TABLE contexts (
  context_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  name TEXT NOT NULL,
  provider TEXT NOT NULL,
  cloud_provider_alias TEXT,
  region TEXT,
  instance_id TEXT,
  public_ip TEXT,
  instance_type TEXT,
  disk_type TEXT,
  disk_size_gb INT,
  kubernetes_context TEXT,
  status TEXT NOT NULL DEFAULT 'provisioning',
  provision_error TEXT,
  -- Placement capacity (#1112): how many environments this context's cluster
  -- may host. Per-context rather than a code-level cluster list, so adding a
  -- cluster to the placement inventory is a POST /v1/contexts call, not a
  -- deploy. 20 is a reasonable default for a dedicated single-node cluster;
  -- operator-overridable at context creation.
  max_environments INT NOT NULL DEFAULT 20,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  CONSTRAINT contexts_name_check CHECK (length(trim(name)) > 0),
  CONSTRAINT contexts_provider_check CHECK (length(trim(provider)) > 0),
  CONSTRAINT contexts_status_check CHECK (status IN ('provisioning', 'running', 'failed')),
  CONSTRAINT contexts_max_environments_check CHECK (max_environments >= 0),
  CONSTRAINT contexts_tenant_context_key UNIQUE (tenant_id, context_id),
  CONSTRAINT contexts_tenant_name_key UNIQUE (tenant_id, name)
);
