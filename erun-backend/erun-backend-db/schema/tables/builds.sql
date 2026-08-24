CREATE TABLE builds (
  build_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  review_id UUID NOT NULL,
  -- RECORDED is a reported build (client POST or a release's own build), which
  -- always names the version it produced. GATE is the merge queue's own
  -- prospective-merge build, which publishes nothing and so mints no version.
  kind TEXT NOT NULL DEFAULT 'RECORDED',
  successful BOOLEAN NOT NULL,
  commit_id TEXT NOT NULL,
  version TEXT,
  -- Populated only for a failed GATE build, in the gate's own words — the
  -- reason a caller-reported RECORDED failure lives wherever that caller's own
  -- CI recorded it, not here.
  failure_detail TEXT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  CONSTRAINT builds_kind_check CHECK (kind IN ('RECORDED', 'GATE')),
  CONSTRAINT builds_commit_id_check CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT builds_version_check CHECK (kind = 'GATE' OR (version IS NOT NULL AND length(trim(version)) > 0)),
  CONSTRAINT builds_failure_detail_check CHECK (successful OR kind <> 'GATE' OR (failure_detail IS NOT NULL AND length(trim(failure_detail)) > 0)),
  CONSTRAINT builds_tenant_build_key UNIQUE (tenant_id, build_id),
  CONSTRAINT builds_tenant_review_build_key UNIQUE (tenant_id, review_id, build_id)
);
