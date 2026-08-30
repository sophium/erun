-- One pending request per verified (issuer, subject): a second submission
-- must update the existing pending row rather than queue a duplicate (issue's
-- §4 abuse bound). Enforced here, not only in application code, so it holds
-- even against a racing double-submit.
CREATE UNIQUE INDEX invite_requests_pending_issuer_subject_idx
  ON invite_requests (issuer, subject)
  WHERE status = 'PENDING';

-- The operator queue lists pending requests by what they name, oldest first.
CREATE INDEX invite_requests_status_tenant_name_idx
  ON invite_requests (status, tenant_name, created_at);
