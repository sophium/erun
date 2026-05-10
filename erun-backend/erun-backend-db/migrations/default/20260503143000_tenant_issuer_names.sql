ALTER TABLE tenant_issuers
  ADD COLUMN name TEXT;

UPDATE tenant_issuers
   SET name = issuer
 WHERE name IS NULL
    OR length(trim(name)) = 0;

ALTER TABLE tenant_issuers
  ALTER COLUMN name SET NOT NULL;

ALTER TABLE tenant_issuers
  ADD CONSTRAINT tenant_issuers_name_check CHECK (length(trim(name)) > 0);

GRANT UPDATE (name) ON tenant_issuers TO erun_tenant;
