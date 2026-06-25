# AGENTS.md

Module-specific guidance for `erun-console/playwright` — the console's end-to-end OIDC sign-in suite. Follow the repository root `AGENTS.md` and `erun-console/AGENTS.md` first.

## Role

This is the **real-IdP** verification for the console's OIDC Authorization Code + PKCE sign-in (issues #606/#684). `erun-console`'s `yarn test` (vitest) mocks the discovery/token endpoints; this suite drives the **whole flow through an actual browser against a real Zitadel v4**, which is the only place that proves the redirect, the Login V2 round-trips, the PKCE code exchange, the API's JWKS signature verification, and the first-sign-in tenant bootstrap all fit together.

It is a **separate yarn package** from the console app (its own `package.json`, `tsconfig`, `eslint.config.mjs`), exactly like `erun-ui/playwright` is separate from `erun-ui/frontend`. The console app's `eslint .` ignores this directory; its `tsc`/`vite`/`vitest` never see it.

## Why a full Zitadel v4 topology (read before "simplifying" it)

Zitadel **v4 has no login UI in the core container** — the interactive login is a *separate* container (`zitadel-login`, "Login V2"). A single `ghcr.io/zitadel/zitadel` container returns `{"code":5,"message":"Not Found"}` at `/ui/v2/login`, so the OIDC `authorize` endpoint has nothing to render. The faithful (and only working) topology is therefore **core + login container + a reverse proxy** unifying them under one origin:

- `zitadel/docker-compose.yml` runs core, `zitadel-login`, Postgres, and an nginx `proxy` that routes `/ui/v2/login` → login and everything else → core, all under `http://localhost:8080`.
- `start-from-init` writes two machine-user PATs to a shared volume: an **org-owner service account** (`ZITADEL_FIRSTINSTANCE_PATPATH`) used for headless provisioning, and the **`IAM_LOGIN_CLIENT`** PAT (`ZITADEL_FIRSTINSTANCE_LOGINCLIENTPATPATH`) the login container authenticates with. (These are `FirstInstance` keys; v4's `DefaultInstance` config has no PAT-to-file path — that mismatch is what makes a single-container headless setup impossible.)
- `ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORDCHANGEREQUIRED: false` gives the admin a permanent password, so the spec drives one deterministic login page (no forced password change, no MFA enrolment).
- `zitadel/provision.sh` reads the service-account PAT and creates the console's OIDC SPA app (PKCE, redirect `http://localhost:5173/`) via the Management API — no UI, no admin login — then writes `.e2e-oidc.env`.

The console + API are issuer-agnostic (`VITE_OIDC_ISSUER`; the API's `OIDCVerifier` does discovery + JWKS with `SkipClientIDCheck`), so the same spec would pass against any compliant IdP; Zitadel is used because it is erunpaas's IdP and its native multi-tenant org model matches the tenant boundary.

## Run

```bash
yarn install            # once
yarn install-browsers   # once — Playwright Chromium
yarn test               # run.sh: brings up the stack, provisions, runs the spec, tears down
yarn test --headed      # watch the browser
```

Prerequisites on PATH: `docker`, `go`, `atlas`, `yarn`. Ports 8080 / 5173 / 17055 / 5544 must be free. `run.sh` is self-contained: it brings up Zitadel, a migrated `erun-backend-api` on its own Postgres, and the console dev server, then tears everything down on exit (including `docker compose down -v`).

## Conventions

- `retries: 0`, `workers: 1`, `fullyParallel: false` — a sign-in that only passes on a retry is a determinism defect to fix, never to mask (root AGENTS.md "No flaky tests"). Wait on observable conditions (URLs, visible elements), never wall-clock sleeps.
- Pin the Zitadel image (`v4.15.3`) — core and `zitadel-login` must be the **same** tag.
- Keep this suite a thin driver: assert on what the operator sees (the Login V2 pages, then the rendered tenant), not on internal endpoints.
