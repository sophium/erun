-- Hosted env-deploy executor (#680, part of #605/#660). Adds the deploy
-- lifecycle to environments: the durable (DBOS-driven) deploy of the runtime
-- chart into a provisioned context's per-env namespace transitions the env
-- through these states, and the console surfaces them.

-- environments gains a deploy lifecycle. deploy_status tracks the durable
-- deploy: 'registered' (config persisted, never deployed) is the default a
-- freshly-created env starts in; the executor transitions it to deploying ->
-- deployed/failed. deploy_error carries the failure reason the console
-- surfaces; deployed_version records the runtime version of the last
-- successful deploy.
ALTER TABLE "environments" ADD COLUMN "deploy_status" text NOT NULL DEFAULT 'registered';
ALTER TABLE "environments" ADD COLUMN "deploy_error" text NULL;
ALTER TABLE "environments" ADD COLUMN "deployed_version" text NULL;
ALTER TABLE "environments" ADD CONSTRAINT "environments_deploy_status_check" CHECK (deploy_status IN ('registered', 'deploying', 'deployed', 'failed'));
