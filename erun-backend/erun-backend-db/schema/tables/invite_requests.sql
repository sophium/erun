-- An invitation request (erun issue: onboarding request/approve queue): a
-- caller who has proven who they are at their own IdP, but is enrolled
-- nowhere, asking either to join an existing tenant or to have a new one
-- registered. issuer/subject are the verified security context's own
-- (iss, sub) — never a caller-supplied value — the same reason invites.issuer
-- is captured server-side rather than trusted from the request body.
CREATE TABLE invite_requests (
  invite_request_id UUID PRIMARY KEY DEFAULT uuidv7(),
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  email TEXT,
  display_name TEXT,
  kind TEXT NOT NULL,
  -- tenant_name is free text, not a tenants.tenant_id foreign key: the
  -- submitter is unauthenticated-to-the-platform and the target tenant may
  -- not exist yet (a CREATE_TENANT request) or the caller must not be able to
  -- probe whether it does (a JOIN_TENANT request must not become a
  -- tenant-name oracle). Resolution against a real tenant happens only at
  -- approval time, inside the approving caller's own authority.
  tenant_name TEXT NOT NULL,
  -- environment_name is the requester's local environment, carried only so
  -- an approval can prefill it; it never resolves anything server-side.
  environment_name TEXT,
  note TEXT,
  status TEXT NOT NULL DEFAULT 'PENDING',
  -- decided_by_user_id references the global users.user_id primary key, not
  -- the tenant-scoped composite, mirroring invites.created_by_user_id: an
  -- OPERATIONS caller deciding a CREATE_TENANT request is not a member of any
  -- tenant this request could name.
  decided_by_user_id UUID,
  decline_reason TEXT,
  -- minted_invite_id links the invite issued on approval (both kinds mint one
  -- — see routes/invite_requests.go), so the requester's own status read can
  -- hand back the same token/link an operator sees.
  minted_invite_id UUID,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  FOREIGN KEY (decided_by_user_id) REFERENCES users (user_id),
  FOREIGN KEY (minted_invite_id) REFERENCES invites (invite_id),
  CONSTRAINT invite_requests_issuer_check CHECK (length(trim(issuer)) > 0),
  CONSTRAINT invite_requests_subject_check CHECK (length(trim(subject)) > 0),
  CONSTRAINT invite_requests_tenant_name_check CHECK (length(trim(tenant_name)) > 0),
  CONSTRAINT invite_requests_kind_check CHECK (kind IN ('JOIN_TENANT', 'CREATE_TENANT')),
  CONSTRAINT invite_requests_status_check CHECK (status IN ('PENDING', 'APPROVED', 'DECLINED')),
  -- A decline with no reason is a dead end (root AGENTS.md's "Smooth,
  -- Seamless, No Dead Ends"), enforced here so no code path can skip it.
  -- The explicit IS NOT NULL matters: length(trim(NULL)) > 0 evaluates to
  -- NULL, and PostgreSQL only rejects a CHECK expression that evaluates to
  -- FALSE, so a bare length(trim(decline_reason)) > 0 would silently let a
  -- NULL reason through (the same shape builds.sql's failure_detail check
  -- already guards against).
  CONSTRAINT invite_requests_decline_reason_check CHECK (status <> 'DECLINED' OR (decline_reason IS NOT NULL AND length(trim(decline_reason)) > 0))
);
