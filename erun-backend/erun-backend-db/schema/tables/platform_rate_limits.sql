-- Platform-wide singleton, not per tenant: its caller (POST /v1/invite-requests)
-- has no tenant yet. The TRUE-fixed boolean PK plus CHECK admits exactly one row.
CREATE TABLE platform_rate_limits (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE,
  -- Per-(issuer, subject) window; the > 0 floor keeps the limiter from being disabled.
  invite_request_window_seconds INT NOT NULL DEFAULT 60 CHECK (invite_request_window_seconds > 0),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  CONSTRAINT platform_rate_limits_singleton_check CHECK (singleton)
);
