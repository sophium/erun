# AGENTS.md

Module-specific guidance for `erun-console`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module role

- `erun-console` is the **hosted web console** for erunpaas (issue #606): a browser SPA served at `console.<base-domain>` where an Operator signs in and views their tenant's erun config. It is a **separate app from `erun-ui`** (the Wails desktop app): the desktop is the _local_ control plane and talks to a Go backend over Wails IPC; the console is the _hosted_ control plane and talks to a backend over HTTP.
- **Architecture (decided).** The console calls **erun-backend-api directly** — that service already carries the OIDC/JWKS auth middleware, the tenant boundary, and `GET /v1/config`. There is **no separate BFF service** in this increment. Keep the console a thin read/render layer over the API contract; shared tenant/environment resolution stays in `erun-common` and is exposed to the console only through the API.
- The console **may later reuse `erun-ui/frontend`'s components**, but it does **not** import the desktop module today and must not modify it. When component reuse lands, extract the shared piece into a shared package rather than importing across app boundaries.
- **Installable as an independent PaaS instance (#603/#606): hardcode no instance-specific names.** The API base, brand, and OIDC issuer are configuration (Vite env / runtime config), never literals in source. `erunpaas.com` / `console.erunpaas.com` are one instance's values, shown as examples.

## What is verifiable here vs. what is a flagged placeholder

This module's verifiable value-prop in its first increment is the **read view**: `GET /v1/config` → `{ tenant, environments[], contexts[] }` rendered for an Operator, proven by a component test against a **mocked** API (`src/config/ConfigView.test.tsx`).

These are **deliberately not implemented** and are flagged as such in source — do not present them as working:

- **OIDC login.** `src/auth/auth.ts` `login()` throws and is documented `TODO(#606): OIDC Authorization Code + PKCE against the platform issuer (Zitadel)`. It needs a live IdP to verify against, so it is a placeholder. The read view is exercised with a dev token from `VITE_DEV_BEARER_TOKEN` (`devBearerToken()`), a local-dev stub only.
- **Driving each env's per-env MCP.** Reached at `mcp.<tenant>-<env>.services.<base-domain>` behind the per-env auth edge; RCE-sensitive (`raw` can `kubectl exec`). A later increment, noted in `src/auth/auth.ts`. Needs a live env to verify.

When you implement either, replace the placeholder with the real flow and add the verification (a real-IdP / live-env test, not a mock) in the same PR.

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
