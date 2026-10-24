-- One pending request per verified (issuer, subject), enforced here so a racing double-submit
-- cannot queue a duplicate.
CREATE UNIQUE INDEX invite_requests_pending_issuer_subject_idx
  ON invite_requests (issuer, subject)
  WHERE status = 'PENDING';

-- The operator queue lists pending requests by what they name, oldest first.
CREATE INDEX invite_requests_status_tenant_name_idx
  ON invite_requests (status, tenant_name, created_at);
