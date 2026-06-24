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

- `src/config/` — the read model. `types.ts` mirrors the `GET /v1/config` JSON (`tenant`, `environments[]`, `contexts[]`); `client.ts` is the typed `fetchConfig(token)` + `ConfigFetchError`; `ConfigView.tsx` renders the tenant header, environments table, and contexts list with empty states.
- `src/auth/` — the flagged OIDC/env-MCP placeholders (see above).
- `src/App.tsx` — wires the token source → `fetchConfig` → `ConfigView`, with loading / signed-out (401) / error states.
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
