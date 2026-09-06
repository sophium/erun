-- Carries the bounded per-build profile (root AGENTS.md #2274) alongside a
-- build's self-reported outcome -- a duration/CPU/throttle/IO summary plus
-- the top costliest steps, never the full step tree. Nullable: a caller on
-- an older erun version, or a build outside a runtime pod, reports none.
ALTER TABLE "builds" ADD COLUMN "profile" JSONB;
