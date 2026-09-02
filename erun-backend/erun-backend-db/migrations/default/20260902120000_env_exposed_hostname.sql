-- Record the MCP edge hostname the deploy Job's chained `erun expose mcp`
-- call actually produced, so a client can discover the edge it is entitled
-- to reach instead of an operator hand-pasting a hostname (#1902). Mirrors
-- expose_error's write point and semantics: NULL means never attempted (no
-- platform ingress IP configured for this deployment) or the chained call
-- failed (see expose_error) -- not "exposed, hostname unknown".
-- Hand-written column add (atlas migrate diff is login-gated on the RLS
-- functions in the source schema); no new table/RLS, so the existing
-- environments row-level security already covers this column.
ALTER TABLE "environments" ADD COLUMN "exposed_hostname" text NULL;
