-- The platform-scoped request-rate-limit configuration (erun issue's §9): a
-- single row for the whole platform, never one per tenant, because the one
-- caller it governs (POST /v1/invite-requests) has no tenant yet. The
-- boolean primary key fixed to TRUE is the standard singleton-table pattern:
-- only one row can ever satisfy both PRIMARY KEY uniqueness and the CHECK, so
-- there is exactly one row to read or write, ever.
CREATE TABLE platform_rate_limits (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE,
  -- invite_request_window_seconds is the post-verification, per-(issuer,
  -- subject) window POST /v1/invite-requests admits one request per. A
  -- floor of 1 keeps the limiter from being disabled by setting it to zero.
  invite_request_window_seconds INT NOT NULL DEFAULT 60 CHECK (invite_request_window_seconds > 0),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  CONSTRAINT platform_rate_limits_singleton_check CHECK (singleton)
);
