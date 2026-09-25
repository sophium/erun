-- The environment definition store: the portable subset of an environment's
-- config, as uploaded from the machine that authored it.
--
-- A sibling table rather than a column on environments, because the lifecycle
-- row is updated on every status transition and a definition has none of that
-- churn: it changes only when somebody uploads one. Keeping it here also gives
-- the definition its own revision counter, which is what a pull compares
-- against to answer "has the platform moved since this machine last synced".
--
-- Keyed by (tenant_id, environment_id) rather than by a surrogate id of its
-- own: a definition is addressed by the environment it describes, never
-- directly, so an extra identity would be one nothing could name.
CREATE TABLE environment_definitions (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  environment_id UUID NOT NULL,
  -- revision increases by one on every upload, from 1 for the definition
  -- written alongside the environment's own registration. A monotonic counter
  -- rather than a timestamp: two uploads can share a clock tick, and a client
  -- comparing "what I synced" against "what is there" needs an ordering it can
  -- compare without trusting a clock it does not own.
  revision INTEGER NOT NULL DEFAULT 1,
  -- definition is the serialized portable subset (erun-common's
  -- PlatformEnvDefinition). JSONB, not TEXT: the platform never interprets the
  -- contents, but a row that is not even an object is a caller error worth
  -- refusing at write time rather than discovering on a pull.
  definition JSONB NOT NULL,
  -- written_by_user_id records who uploaded this revision. It cannot be
  -- defaulted from the request body for the same reason tenant_id cannot; see
  -- erun_current_user_id().
  written_by_user_id UUID NOT NULL DEFAULT erun_current_user_id(),
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, environment_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  -- The definition belongs to the environment and has no life without it:
  -- deleting an environment takes its stored definition with it, rather than
  -- leaving a row that names an environment nobody can look up.
  CONSTRAINT environment_definitions_tenant_environment_fkey
    FOREIGN KEY (tenant_id, environment_id) REFERENCES environments (tenant_id, environment_id) ON DELETE CASCADE,
  CONSTRAINT environment_definitions_revision_check CHECK (revision > 0),
  CONSTRAINT environment_definitions_definition_object_check CHECK (jsonb_typeof(definition) = 'object')
);
