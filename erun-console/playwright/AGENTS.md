# AGENTS.md

Module-specific guidance for `erun-console/playwright` — the console's end-to-end OIDC sign-in suite. Follow the repository root `AGENTS.md` and `erun-console/AGENTS.md` first, then apply this file for work in this subtree.

## Role

This is the **real-IdP** verification for the console's OIDC Authorization Code + PKCE sign-in. `erun-console`'s `yarn test` (vitest) mocks the discovery and token endpoints; this suite drives the **whole flow through an actual browser against a real Zitadel v4**, which is the only place that proves the redirect, the Login V2 round-trips, the PKCE code exchange, the API's JWKS signature verification, and the first-sign-in tenant bootstrap all fit together. A mock IdP here would prove nothing the unit tests do not already.

It is a **separate yarn package** from the console app (its own `package.json`, `tsconfig`, `eslint.config.mjs`), exactly like `erun-ui/playwright` is separate from `erun-ui/frontend`. The console app's `eslint .` ignores this directory, and its `tsc`/`vite`/`vitest` never see it.

## Gating

Opt-in and skipped by default, the same convention as the backend's live env-deploy gate (`ERUN_E2E_ENV_DEPLOY=1`): the spec calls `test.skip()` unless `ERUN_E2E_CONSOLE_OIDC=1`, and `run.sh` is the only thing that sets it — after it has actually stood the stack up. A suite that silently assumes a running IdP is worse than no suite, so `yarn playwright test` on its own skips rather than failing against nothing.

## Why a full Zitadel v4 topology (read before "simplifying" it)

Zitadel **v4 has no login UI in the core container** — the interactive login is a _separate_ container (`zitadel-login`, "Login V2"). A single `ghcr.io/zitadel/zitadel` container returns `{"code":5,"message":"Not Found"}` at `/ui/v2/login`, so the OIDC `authorize` endpoint has nothing to render. The faithful (and only working) topology is therefore **core + login container + a reverse proxy** unifying them under one origin:

- `zitadel/stack.sh up` runs Postgres, core, `zitadel-login`, and an nginx `proxy` that routes `/ui/v2/login` → login and everything else → core, all under `http://localhost:8080`. Plain `docker run`, not compose: `docker` is the only container tool this repository's harnesses assume.
- Readiness is polled over **HTTP through the proxy**, not via container healthchecks — `docker run --health-cmd` always runs through `/bin/sh` and the Zitadel images are distroless. The proxy therefore starts before its upstreams; nginx's `resolver` makes upstreams resolve per request, so it tolerates that.
- `start-from-init` writes two machine-user PATs to a shared volume: an **org-owner service account** (`ZITADEL_FIRSTINSTANCE_PATPATH`) used for headless provisioning, and the **`IAM_LOGIN_CLIENT`** PAT (`ZITADEL_FIRSTINSTANCE_LOGINCLIENTPATPATH`) the login container authenticates with. (These are `FirstInstance` keys; v4's `DefaultInstance` config has no PAT-to-file path — that mismatch is what makes a single-container headless setup impossible.)

## Provision and clean up its own identities

The run owns everything it signs in with, so it is repeatable and leaves the issuer as it found it:

- `zitadel/provision.sh` creates **this run's own** project, OIDC SPA app (PKCE, redirect `http://localhost:5173/`) and human login user through the Management API — no UI, no admin sign-in — and records every created id in `.e2e-oidc.env`.
- The login user is created via `users/human/_import` (the endpoint that takes a password and can clear `passwordChangeRequired`), and the org's login policy is pinned to password-only (`PASSWORDLESS_TYPE_NOT_ALLOWED`, `forceMfa: false`). Without that pinning the org inherits the instance default, which offers passkey enrolment after the password step — the exact non-determinism that makes naive UI automation flaky.
- `zitadel/deprovision.sh` deletes the user, app and project by the recorded ids. Tearing the containers down would erase them anyway; doing it explicitly is what proves the run cleans up, and keeps the harness usable against a Zitadel that outlives one run.

The console and API are issuer-agnostic (`VITE_OIDC_ISSUER`; the API's verifier does discovery + JWKS), so the same spec would pass against any compliant IdP; Zitadel is used because it is erunpaas's IdP and its native multi-tenant org model matches the tenant boundary.

## Run

```bash
yarn install            # once
yarn install-browsers   # once — Playwright Chromium
yarn test               # run.sh: brings up the stack, provisions, runs the spec, tears it all down
yarn test --headed      # watch the browser
```

Prerequisites on PATH: `docker`, `go`, `atlas`, `yarn`, `python3`. Ports 8080 / 5173 / 17055 / 5544 must be free — `run.sh` refuses to start when one is taken, rather than testing against whatever is already there.

## Conventions

- `retries: 0`, `workers: 1`, `fullyParallel: false` — a sign-in that only passes on a retry is a determinism defect to fix, never to mask. Wait on observable conditions (URLs, visible elements, HTTP status), never wall-clock sleeps.
- **Background services are killed by process group.** `$!` on a subshell is the subshell, not the server it starts; a dev server that survives teardown will serve the _next_ run the _previous_ run's client id, and the failure looks like a broken redirect rather than a leak. `setsid` + `kill -- -$PGID`, plus the free-port precondition, is what keeps that from recurring.
- Pin the Zitadel image (`v4.15.3`) — core and `zitadel-login` must be the **same** tag.
- Keep this suite a thin driver: assert on what the operator sees (the Login V2 pages, then the rendered tenant), not on internal endpoints.
