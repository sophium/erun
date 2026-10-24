CREATE TABLE builds (
  build_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  -- NULL for a build reported with no review attached.
  review_id UUID,
  -- SET NULL on environment delete so build history outlives it; the composite FK keeps
  -- the reference within the tenant.
  environment_id UUID,
  -- GATE is retired; historical GATE rows must never be deleted or rewritten, because
  -- reviews.last_merged_build_id anchors queue verification on them.
  kind TEXT NOT NULL DEFAULT 'RECORDED',
  successful BOOLEAN NOT NULL,
  commit_id TEXT NOT NULL,
  version TEXT,
  -- Why a failed build failed, in the reporter's words; historical failed GATE rows always have one.
  failure_detail TEXT,
  -- Bounded self-reported profile summary, never the full step tree; NULL when none was collected.
  profile JSONB,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  -- SET NULL (environment_id) only: nulling the whole key would violate tenant_id NOT NULL.
  CONSTRAINT builds_tenant_environment_fkey
    FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id)
    ON DELETE SET NULL (environment_id),
  CONSTRAINT builds_kind_check CHECK (kind IN ('RECORDED', 'GATE')),
  CONSTRAINT builds_gate_requires_review_check CHECK (kind <> 'GATE' OR review_id IS NOT NULL),
  CONSTRAINT builds_commit_id_check CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT builds_version_check CHECK (
    (kind = 'RECORDED' AND version IS NOT NULL AND length(trim(version)) > 0)
    OR (kind = 'GATE' AND version IS NULL)
  ),
  CONSTRAINT builds_failure_detail_check CHECK (successful OR kind <> 'GATE' OR (failure_detail IS NOT NULL AND length(trim(failure_detail)) > 0)),
  CONSTRAINT builds_tenant_build_key UNIQUE (tenant_id, build_id),
  CONSTRAINT builds_tenant_review_build_key UNIQUE (tenant_id, review_id, build_id)
);
