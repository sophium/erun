# AGENTS.md

Module-specific guidance for `erun-console`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module role

- `erun-console` is the **hosted web console** for erunpaas (issue #606): a browser SPA served at `console.<base-domain>` where an Operator signs in and views their tenant's erun config. It is a **separate app from `erun-ui`** (the Wails desktop app): the desktop is the _local_ control plane and talks to a Go backend over Wails IPC; the console is the _hosted_ control plane and talks to a backend over HTTP.
- **Architecture (decided).** The console calls **erun-backend-api directly** — that service already carries the OIDC/JWKS auth middleware, the tenant boundary, and `GET /v1/config`. There is **no separate BFF service** in this increment. Keep the console a thin read/render layer over the API contract; shared tenant/environment resolution stays in `erun-common` and is exposed to the console only through the API.
- The console **may later reuse `erun-ui/frontend`'s components**, but it does **not** import the desktop module today and must not modify it. When component reuse lands, extract the shared piece into a shared package rather than importing across app boundaries.
- **Installable as an independent PaaS instance (#603/#606): hardcode no instance-specific names.** The API base, brand, and OIDC issuer are configuration (Vite env / runtime config), never literals in source. `erunpaas.com` / `console.erunpaas.com` are one instance's values, shown as examples.

## What is verifiable here vs. what is a flagged placeholder

This module's verifiable value-prop in its first increment is the **read view**: `GET /v1/config` → `{ tenant, environments[], contexts[] }` rendered for an Operator, proven by a component test against a **mocked** API (`src/config/ConfigView.test.tsx`).

**OIDC sign-in is implemented** (`src/auth/auth.ts`): the real Authorization Code + PKCE flow — `beginLogin` redirects to the issuer's authorize endpoint (discovery-resolved, S256 PKCE, state CSRF guard), `completeLogin` exchanges the callback code + verifier for the `id_token` and holds it as the bearer. It is config-driven (`VITE_OIDC_ISSUER` + `VITE_OIDC_CLIENT_ID`); unset → the `VITE_DEV_BEARER_TOKEN` dev fallback. The PKCE mechanics (callback exchange, state validation, config gating, fallback) are unit-tested in `src/auth/auth.test.ts` (mocked discovery/token endpoints); the IdP is real and **local** — Zitadel runs in Docker, not a hosted dependency (recipe below). The full browser sign-in click-through (provisioned Zitadel OIDC app + the Zitadel login UI) is driven manually / by a Playwright e2e against the local Zitadel; the API side is already proven to accept any-issuer JWTs (its `OIDCVerifier` does discovery + JWKS with `SkipClientIDCheck`).

Still **deliberately not implemented** (flagged in source):

- **Driving each env's per-env MCP.** Reached at `mcp.<tenant>-<env>.services.<base-domain>` behind the per-env auth edge; RCE-sensitive (`raw` can `kubectl exec`). A later increment, noted in `src/auth/auth.ts`. Needs a live env to verify.

When you implement it, replace the placeholder with the real flow and add the verification (a real-IdP / live-env test, not a mock) in the same PR.

## Layout

- `src/config/` — the read model. `types.ts` mirrors the `GET /v1/config` JSON (`tenant`, `environments[]`, `contexts[]`), including each context's provisioning `status`/`provisionError`; `client.ts` is the typed `fetchConfig(token)` + `ConfigFetchError`, plus the provisioning writes (`setCloudProviderAlias`, `createContext`, `getContext`); `ConfigView.tsx` renders the tenant header, environments table, and the contexts list with empty states and a per-context status badge.
- `src/provision/` — the cloud-context provisioning surface (issue #605/#676): `controller.ts` is the thin request/poll controller (a `useProvisionController` hook that sequences the client calls — register a BYO-cloud alias, `POST /v1/contexts`, then poll `GET /v1/contexts/{id}` to `running`/`failed`), and `ProvisionPanel.tsx` is the render layer (alias form + create-context form + live status). This is **verifiable**, not a placeholder: `ProvisionPanel.test.tsx` exercises the alias PUT and the create→poll flow against a mocked `fetch`, the same weight as the read view's test. The live OIDC token still comes from the dev stub — only the auth flow is flagged.
- `src/environments/` — the env-registration surface (issue #606): `controller.ts` is the thin create-request controller (`useRegisterEnvController`, calls `createEnvironment` = `POST /v1/environments`, then invokes `onRegistered` so the parent refreshes the read model), and `RegisterEnvPanel.tsx` is the render layer (name + type + cloud-context picker + optional runtime version). `RegisterEnvPanel.test.tsx` exercises the POST + the 409 quota error against a mocked `fetch`. This closes the alias → context → register env → deploy loop inside the console.
- `src/deploy/` — the runtime-deploy surface (issue #680): `controller.ts` is the per-env trigger/poll controller (`useDeployController`, calls `deployEnvironment` = `POST /v1/environments/{id}/deploy`, then polls `getEnvironment` to `deployed`/`failed`), and `DeployPanel.tsx` is the render layer (per runtime env: Deploy + optional version + live status). `DeployPanel.test.tsx` exercises the deploy→poll flow against a mocked `fetch`.
- `src/auth/` — the flagged OIDC/env-MCP placeholders (see above).
- `src/App.tsx` — wires the token source → `fetchConfig` → `ConfigView`, with loading / signed-out (401) / error states, and renders `ProvisionPanel`, `RegisterEnvPanel`, and `DeployPanel` below the read view when a token is present; a successful registration re-runs `fetchConfig` so the new env appears in the config view + deploy panel.
- `src/test/setup.ts` — Testing Library jest-dom matchers for vitest.

## Toolchain

- Vite + React 19 + strict TypeScript, **Yarn** (`yarn@1.22.22`), matching `erun-ui/frontend`'s style. Do not introduce `npm`/`pnpm` lockfiles.
- ESLint flat config (`eslint.config.mjs`) mirrors `erun-ui/frontend`: type-aware `strictTypeChecked` + `stylisticTypeChecked`, `complexity:10`, `max-lines-per-function:100`, `max-lines:500`, React hooks/refresh, `jsx-a11y`. Every rule is `error`; fix findings by correcting the code — never disable, suppress, or downgrade a rule.
- Prettier (`.prettierrc.json`) is the formatter, same config as `erun-ui/frontend`.
- Styling is plain CSS (`src/styles.css`) for this first increment — intentionally minimal, no Tailwind/shadcn yet. Add a design system only when the surface grows enough to need one.

## Validation

Run all of these from `erun-console/` for any change to this module:

```bash
yarn install
yarn typecheck      # tsc --noEmit
yarn lint           # eslint .
yarn build          # vite build
yarn test           # vitest run — the component test
```

- `yarn test` (vitest + `@testing-library/react`, jsdom) is the increment's real verification: it mocks `fetch` and asserts `ConfigView` renders the read model, empty states, and the 401 sign-in prompt.
- A Playwright e2e harness like `erun-ui/playwright/` (boot the SPA, drive a seeded backend) is the **follow-up** to this component-level test — appropriate once the OIDC flow and a runnable backend stub exist. The component test is the right weight for this first increment.

## Running against a real erun-backend-api (dev / e2e)

The console can be driven against a **live** `erun-backend-api` with no live IdP, the same way the MCP edge is tested (issue #674): the API trusts a `file://` desktop key, so a desktop-signed token authenticates the read view end to end.

- `vite.config.ts` proxies `/v1/...` to `VITE_API_PROXY_TARGET` (default `http://127.0.0.1:17033`) so `yarn dev` fetches **same-origin** — the API sets no CORS headers by design, and the dev proxy forwards server-side, so there is no browser preflight. In production the console is served same-origin behind the API edge, so no proxy is needed.
- `VITE_DEV_BEARER_TOKEN` is the bearer token `App.tsx` presents. For a real-API run it must be a token the API trusts: either an OIDC JWT, or a desktop-signed `file://` token (EdDSA, audience `erun-api`) when the API is started with `ERUN_API_DESKTOP_PUBLIC_KEY_PATH` pointing at the matching public key. See [`api-protocol.md` § Sign-in](../erun-docs/docs/agent-reference/api-protocol.md) for the token model.

```bash
# point yarn dev at a running API on :17055 with a desktop-signed token
VITE_API_PROXY_TARGET=http://127.0.0.1:17055 VITE_DEV_BEARER_TOKEN=<token> yarn dev
```

Unlike `yarn test` (mocked `fetch`), this renders the **real** `{ tenant, environments[], contexts[] }` the API serves from Postgres — the verification that closed the option-2 console↔API check for #606/#658.

## Running the OIDC sign-in against a local Zitadel

The OIDC issuer is **not** a hosted dependency — Zitadel runs locally in Docker, the same way floci (AWS) and Lima (k3s) stand in for cloud deps elsewhere. Bring it up with its own Postgres on a docker network, cold-started with `start-from-init`:

```bash
docker network create zitadel-net
docker run -d --name zitadel-db --network zitadel-net \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=zitadel postgres:18
docker run -d --name zitadel --network zitadel-net -p 8080:8080 \
  -e ZITADEL_DATABASE_POSTGRES_HOST=zitadel-db -e ZITADEL_DATABASE_POSTGRES_PORT=5432 \
  -e ZITADEL_DATABASE_POSTGRES_DATABASE=zitadel \
  -e ZITADEL_DATABASE_POSTGRES_USER_USERNAME=postgres -e ZITADEL_DATABASE_POSTGRES_USER_PASSWORD=postgres -e ZITADEL_DATABASE_POSTGRES_USER_SSL_MODE=disable \
  -e ZITADEL_DATABASE_POSTGRES_ADMIN_USERNAME=postgres -e ZITADEL_DATABASE_POSTGRES_ADMIN_PASSWORD=postgres -e ZITADEL_DATABASE_POSTGRES_ADMIN_SSL_MODE=disable \
  -e ZITADEL_EXTERNALSECURE=false -e ZITADEL_EXTERNALDOMAIN=localhost -e ZITADEL_EXTERNALPORT=8080 -e ZITADEL_TLS_ENABLED=false \
  ghcr.io/zitadel/zitadel:latest start-from-init --masterkey "MasterkeyNeedsToHave32Characters" --tlsMode disabled
# OIDC discovery + JWKS come up at http://localhost:8080/.well-known/openid-configuration
```

Then, in the Zitadel console (`http://localhost:8080/ui/console`), create a Project → an **OIDC application** of type **User Agent / SPA** with **PKCE** and redirect URI `http://localhost:5173/` (the `yarn dev` origin), and note its **client id**. Point the console at it and run the API with Zitadel in its issuer allow-list (the issuer is `http://localhost:8080`):

```bash
# console
VITE_OIDC_ISSUER=http://localhost:8080 VITE_OIDC_CLIENT_ID=<client-id> yarn dev
# api (trusts the Zitadel issuer; empty allow-list also accepts any resolvable issuer)
ERUN_OIDC_ALLOWED_ISSUERS=http://localhost:8080 … eapi
```

The API's `OIDCVerifier` discovers Zitadel's JWKS and verifies the `id_token` signature (`SkipClientIDCheck`, so audience is the caller's policy); on an empty database the first Zitadel sign-in bootstraps the `OPERATIONS` tenant + first user and registers the issuer. Note Zitadel's first login forces a password change + may prompt MFA enrolment, which a Playwright e2e must drive (or provision a pre-verified, change-not-required user via the management API).
