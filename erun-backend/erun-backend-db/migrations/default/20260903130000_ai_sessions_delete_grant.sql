-- ai_sessions was never granted DELETE, unlike every other tenant-owned
-- table with a FOR ALL RLS policy (schema/roles.sql's shared grant list).
-- The retention sweep needs it to prune exited sessions (see
-- erun-backend-db/AGENTS.md, "AI Sessions"). Hand-written for the same
-- reason 20260831120000_ai_sessions.sql was: atlas migrate diff is
-- login-gated on the RLS functions in the source schema.

GRANT DELETE ON "ai_sessions" TO erun_tenant, erun_operations;
