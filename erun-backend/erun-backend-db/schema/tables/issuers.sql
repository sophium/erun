CREATE TABLE issuers (
  issuer TEXT PRIMARY KEY,
  org_field_key TEXT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ
);
