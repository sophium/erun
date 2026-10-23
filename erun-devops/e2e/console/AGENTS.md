# AGENTS.md

The deployed console's e2e suite. Follow root, `erun-devops/AGENTS.md` and
`erun-console/AGENTS.md`. This is a separate Yarn package run only through its
`Dockerfile`; it is not part of any component build, release or merge gate.

## Role

Proves what only a deployed environment can: the console exposed as the
`console` service over its real certificate, serving the deployed version, the
OIDC sign-in through the deployment's own IdP, and a WebSocket attach over an
exposed MCP edge. Anything that works against the console's own bundle belongs
in `erun-console/playwright` and the console image's `test` stage instead.

## Run

- `erun e2e --component console` resolves the exposed URL and deployed version
  and builds the `e2e` stage with `ERUN_E2E_BASE_URL` and `ERUN_E2E_VERSION`.
  The Dockerfile header shows the equivalent `docker build`.
- The signed-in tests need the e2e operator account and an attachable
  environment, supplied as the `e2e_login_name`, `e2e_login_password` and
  `e2e_attach_environment` BuildKit secrets (`.erun/config.yaml` `e2e.secrets`).
  Without them those tests skip and name the secret; the unauthenticated
  checks still run.
- Local checks: `yarn install`, then `yarn typecheck && yarn lint &&
yarn format:check`. The `e2e` stage runs these before the suite.

## Conventions

- TLS is verified: never set `ignoreHTTPSErrors` or a literal `baseURL`
  (`erun e2e` refuses both). The URL comes only from `ERUN_E2E_BASE_URL`.
- The suite runs against a real, shared deployment. Only read, sign in and out,
  and re-attach to the fixed session id; never register tenants or contexts,
  deploy, or start and stop environments — those suites stay local in
  `erun-console/playwright`.
- Keep the Playwright image tag in the `Dockerfile` equal to the
  `@playwright/test` version `yarn.lock` resolves.
- No retries: a pass that needs one is a determinism defect.
