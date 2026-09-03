CREATE TABLE builds (
  build_id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  -- NULL for an ordinary `erun build` that reports itself with no review
  -- attached (erun#1954) -- a review association is one reason a build
  -- happened, not its identity.
  review_id UUID,
  -- The environment the build ran in, when the caller reports one. NULL for
  -- a review-linked build (RECORDED/GATE builds reported through the
  -- review-nested route carry no environment today) and SET NULL if the
  -- environment is later deleted -- an append-only build history should
  -- outlive the row it described, the same choice usage_events.environment_id
  -- already made.
  environment_id UUID,
  -- RECORDED is a reported build (client POST or a release's own build), which
  -- always names the version it produced. GATE is the merge queue's own
  -- prospective-merge build, which publishes nothing and so mints no version,
  -- and always belongs to the review it gates.
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
  FOREIGN KEY (environment_id) REFERENCES environments (environment_id) ON DELETE SET NULL,
  CONSTRAINT builds_kind_check CHECK (kind IN ('RECORDED', 'GATE')),
  CONSTRAINT builds_gate_requires_review_check CHECK (kind <> 'GATE' OR review_id IS NOT NULL),
  CONSTRAINT builds_commit_id_check CHECK (length(trim(commit_id)) > 0),
  CONSTRAINT builds_version_check CHECK (kind = 'GATE' OR (version IS NOT NULL AND length(trim(version)) > 0)),
  CONSTRAINT builds_failure_detail_check CHECK (successful OR kind <> 'GATE' OR (failure_detail IS NOT NULL AND length(trim(failure_detail)) > 0)),
  CONSTRAINT builds_tenant_build_key UNIQUE (tenant_id, build_id),
  CONSTRAINT builds_tenant_review_build_key UNIQUE (tenant_id, review_id, build_id)
);
