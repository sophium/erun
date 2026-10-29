-- A verified but unenrolled caller asking to join or create a tenant. issuer/subject come
-- from the verified security context, never the request body.
CREATE TABLE invite_requests (
  invite_request_id UUID PRIMARY KEY DEFAULT uuidv7(),
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  email TEXT,
  display_name TEXT,
  kind TEXT NOT NULL,
  -- Free text, not an FK: the tenant may not exist yet, and a JOIN request must not become
  -- a tenant-name oracle. Resolved only at approval.
  tenant_name TEXT NOT NULL,
  -- Only prefills the approval; never resolved server-side.
  environment_name TEXT,
  note TEXT,
  status TEXT NOT NULL DEFAULT 'PENDING',
  -- Global users key: an OPERATIONS decider need not belong to any named tenant.
  decided_by_user_id UUID,
  decline_reason TEXT,
  -- The invite minted on approval, so the requester's status read returns the same link.
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
  -- IS NOT NULL is required: length(trim(NULL)) is NULL, and a NULL CHECK result passes.
  CONSTRAINT invite_requests_decline_reason_check CHECK (status <> 'DECLINED' OR (decline_reason IS NOT NULL AND length(trim(decline_reason)) > 0))
);
