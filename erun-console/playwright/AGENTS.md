# AGENTS.md

Real-infrastructure console tests. Follow root and `erun-console/AGENTS.md`.
This is a separate Yarn package, not part of the app's Vitest/tsc collection.

## Role

Each suite stands up only its own dependencies and proves a boundary mocked
fetch/WebSocket tests cannot:

| Runner | Real boundary | Opt-in variable |
| --- | --- | --- |
| `yarn test` / `run.sh` | Browser OIDC/PKCE, Zitadel, JWKS, first-user bootstrap | `ERUN_E2E_CONSOLE_OIDC` |
| `yarn test:mcp-operate-scope` | Cross-origin JSON-RPC and capability refusal | `ERUN_E2E_CONSOLE_MCP_OPERATE` |
| `yarn test:mcp-attach-session` | TLS WebSocket, fresh-pod dtach/PTY | `ERUN_E2E_CONSOLE_MCP_ATTACH` |
| `yarn test:rest-surfaces` | Alias/context/tenant API writes | `ERUN_E2E_CONSOLE_REST` |

## Gating

Scripts set their opt-in variable only after provisioning dependencies. A bare
Playwright invocation skips unconfigured live suites; do not assume ambient
services or use these opt-ins to hide failures in a configured suite.

## Why a full Zitadel v4 topology (read before "simplifying" it)

- Keep Postgres + core + Login V2 + nginx under one origin; core alone has no v4
  login UI. Use plain Docker, not an additional compose prerequisite.
- Probe HTTP through nginx, not distroless-container shell healthchecks.
  Preserve request-time DNS resolution so nginx may start before upstreams.
- Keep org-owner and IAM_LOGIN_CLIENT PATs distinct and provisioned through
  FirstInstance PAT paths. Core/login images must use the same pinned version.

## Provision and clean up its own identities

Create a run-owned project, PKCE SPA app, and user; record IDs in
`.e2e-oidc.env`. Use password-ready user import and a password-only/non-MFA login
policy so enrolment prompts cannot vary. Delete those exact IDs on teardown,
even when the issuer survives the run. Never reuse operator identities.

## Run

- Install with `yarn install` and `yarn install-browsers`.
- Use the runner table above; headed variants are in package.json
  (`yarn test --headed` for OIDC).
- Prerequisites: Docker, Go, Atlas, Yarn, OpenSSL, Python; Node for attach's TLS
  front. Each runner owns a disjoint port set and must refuse occupied ports,
  never test against whatever already answers.

## Conventions

- Keep one worker, no full parallelism, and zero retries. Wait on observable
  URLs/elements/HTTP state, never wall-clock sleeps or retries-until-green.
- Teardown background services by process group; a subshell PID is not its server.
  Preserve the free-port precondition and cleanup traps.
- Specs are thin UI drivers asserting what an operator sees, not only endpoint
  internals. Shared UX and safety rules still apply.

## The MCP operate-scope e2e

- Run real Postgres, API signing, and emcp. The throwaway rootful container supplies
  the fixed trusted-key filesystem path, not permission to modify host keys.
- Authenticate as an ordinary TenantUser, not the bootstrap admin. Allowed
  operate calls must reach business logic; admin-only raw/delete/terraform/init
  must remain unregistered/refused. Also verify actionable admin-mint denial.
- Keep the browser and edge cross-origin. Non-browser success does not exercise
  CORS/SDK request-origin protection; their implementation belongs to MCP guidance.

## The WebSocket attach-session e2e

- Keep a real dtach/PTY and self-signed TLS front: the client deliberately requires
  wss. Certificate relaxation belongs only to this test context.
- Start without a prior CLI session directory. Prove mint → connect → marker
  echo/scrollback → disconnect; handshake success alone is not attach success.

## The REST-surfaces e2e

- Run real Postgres/API/console with `ERUN_SECRETS_KEY` configured so alias
  encryption is exercised. The unconfigured route's named 501 is a separate case.
- No ContextProvisioner is wired here: assert context registration/polling, never
  claim it reached running. Terminal deployment needs a real job/cluster runner.
- Identity administration against a real Management API remains a disclosed gap;
  OIDC sign-in's Zitadel topology does not itself prove administration.
- Local edge/TLS tests do not prove public services-zone ingress.
