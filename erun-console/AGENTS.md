# AGENTS.md

Module-specific guidance for `erun-console`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module role

- `erun-console` is the **hosted web console** for erunpaas (issue #606): a browser SPA served at `console.<base-domain>` where an Operator signs in and views their tenant's erun config. It is a **separate app from `erun-ui`** (the Wails desktop app): the desktop is the _local_ control plane and talks to a Go backend over Wails IPC; the console is the _hosted_ control plane and talks to a backend over HTTP.
- **Architecture (decided).** The console calls **erun-backend-api directly** — that service already carries the OIDC/JWKS auth middleware, the tenant boundary, and `GET /v1/config`. There is **no separate BFF service** in this increment. Keep the console a thin read/render layer over the API contract; shared tenant/environment resolution stays in `erun-common` and is exposed to the console only through the API.
- The console does **not** import `erun-ui/frontend` and must not modify it. Component reuse with the desktop happens through the shared `erun-kit` workspace package (issue #1211) — never by importing across the two app boundaries directly. See `erun-kit/AGENTS.md` for what lives there and how to consume it.
- **Installable as an independent PaaS instance (#603/#606): hardcode no instance-specific names.** The API base, brand, and OIDC issuer are configuration (Vite env / runtime config), never literals in source. `erunpaas.com` / `console.erunpaas.com` are one instance's values, shown as examples.

## What is verifiable here vs. what is a flagged placeholder

This module's verifiable value-prop in its first increment is the **read view**: `GET /v1/config` → `{ tenant, environments[], contexts[] }` rendered for an Operator, proven by a component test against a **mocked** API (`src/config/ConfigView.test.tsx`).

**OIDC sign-in is no longer a placeholder.** `src/auth/auth.ts` implements the real Authorization Code + PKCE flow against `VITE_OIDC_ISSUER` / `VITE_OIDC_CLIENT_ID`, and it is verified two ways: `src/auth/auth.test.ts` locks the PKCE mechanics against mocked discovery/token endpoints, and `playwright/` drives the whole browser sign-in against a **real Zitadel v4** through to the rendered tenant config (see `playwright/AGENTS.md`). `VITE_DEV_BEARER_TOKEN` remains as the fallback when OIDC is unconfigured — a local-dev path only.

This is **deliberately not implemented** and is flagged as such in source — do not present it as working:

- **Driving each env's per-env MCP.** Minting and surfacing a per-env MCP bearer token is now implemented and verified (`src/mcp/` — see Layout; the backend signs it, so the browser holds no key). What stays deferred is _driving_ that env's MCP tools over the live edge (`mcp.<tenant>-<env>.services.<base-domain>`, RCE-sensitive — `raw` can `kubectl exec`): it needs a deployed env carrying the backend's public key to verify against. Noted in `src/auth/auth.ts`.

When you implement it, replace the placeholder with the real flow and add the verification (a live-env test, not a mock) in the same PR.

## Layout

- `src/config/` — the read model. `types.ts` mirrors the `GET /v1/config` JSON (`tenant`, `environments[]`, `contexts[]`), including each context's provisioning `status`/`provisionError`; `client.ts` is the typed `fetchConfig(token)` + `ConfigFetchError`, plus the provisioning writes (`setCloudProviderAlias`, `createContext`, `getContext`); `ConfigView.tsx` renders the tenant header, environments table, and the contexts list with empty states and a per-context and per-environment provisioning status badge.
- `src/provision/` — the cloud-context provisioning surface (issue #605/#676): `controller.ts` is the thin request/poll controller (a `useProvisionController` hook that sequences the client calls — register a BYO-cloud alias, `POST /v1/contexts`, then poll `GET /v1/contexts/{id}` to `running`/`failed`), and `ProvisionPanel.tsx` is the render layer (alias form + create-context form + live status). This is **verifiable**, not a placeholder: `ProvisionPanel.test.tsx` exercises the alias PUT and the create→poll flow against a mocked `fetch`, the same weight as the read view's test. The live OIDC token still comes from the dev stub — only the auth flow is flagged.
- `src/mcp/` — the per-env MCP access surface (issue #686): `controller.ts` is a thin `useMcpTokenController` hook that mints a token on demand via `requestMcpToken` (`POST /v1/environments/{id}/mcp-token`), and `MCPAccessPanel.tsx` is the render layer (env picker → mint → surface the bearer token + audience). **Verifiable**, not a placeholder: `MCPAccessPanel.test.tsx` exercises the mint + the 501 (unconfigured backend) against a mocked `fetch`. The token client lives in `config/client.ts` alongside the other writes.
- `src/identity/` — the IdP-identity administration surface (issue #1209): `client.ts` is the typed `/v1/identity/*` client (list/enroll/deactivate/reactivate users, get/update org settings); `controller.ts` holds `useUsersController` and `useOrgSettingsController`; `UsersPanel.tsx` and `OrgSettingsPanel.tsx` are the two render layers. Both panels are rendered from `App.tsx` only when `config.tenant.type === 'OPERATIONS'`, mirroring the backend's own restriction — a `COMPANY`-tenant Operator never sees the forms at all. **The IdP credential (Zitadel's org-owner PAT) is backend-only**: the console only ever calls erun-backend-api, never Zitadel directly. **Verifiable**, not a placeholder: `UsersPanel.test.tsx`/`OrgSettingsPanel.test.tsx` exercise list/enroll/deactivate/reactivate and get/update against a mocked `fetch`.
- `src/auth/` — the OIDC Authorization Code + PKCE sign-in (`oidcConfig` / `beginLogin` / `completeLogin` / `resolveToken` / `signOut`) and the dev-token fallback, plus the note on the still-deferred env-MCP driving (see above).
- `playwright/` — a **separate yarn package** holding the real-Zitadel browser sign-in e2e. Opt-in behind `ERUN_E2E_CONSOLE_OIDC=1`; `playwright/run.sh` is what sets it, after standing up Zitadel + a migrated API + the console. See `playwright/AGENTS.md`.
- `src/App.tsx` — wires the token source → `fetchConfig` → `ConfigView`, with loading / signed-out (401) / error states, and renders `ProvisionPanel` below the read view when a token is present.
- `src/test/setup.ts` — Testing Library jest-dom matchers for vitest.

## Permission degradation

- **Degrade by permission, and never by role name.** `GET /v1/whoami` reports the caller's effective permission set (`capabilities`: canonical `{method, path}` pairs, specced in `erun-docs/docs/agent-reference/api-protocol.md#capability-set`). That set is the only input a surface may gate on: a role's name says nothing about what a tenant granted it, so a control shown because a role is literally called `WriteAll` is wrong for every custom role a tenant defines.
- The rules are the same ones `erun-ui/AGENTS.md` states for the desktop, because a permission-shaped empty state should read identically in both clients: a list the caller may not read is not an empty list, a read they may not make is not attempted, a control they may not use does not render (with the missing access still named somewhere visible), partial access degrades partially rather than blanking the whole view, and "nothing exists yet" / "nothing matches the filter" / "you may not see this" stay three distinguishable states.
- Express "may I?" through the shared capability shape rather than a console-local predicate. Until the shared frontend kit lands (#1211) the console has no capability-gated surface yet; the first one to need it adds the shape to the shared kit, not to a component.

## Toolchain

- Vite + React 19 + strict TypeScript, **Yarn** (`yarn@1.22.22`), matching `erun-ui/frontend`'s style. Do not introduce `npm`/`pnpm` lockfiles.
- ESLint flat config (`eslint.config.mjs`) mirrors `erun-ui/frontend`: type-aware `strictTypeChecked` + `stylisticTypeChecked`, `complexity:10`, `max-lines-per-function:100`, `max-lines:500`, React hooks/refresh, `jsx-a11y`. Every rule is `error`; fix findings by correcting the code — never disable, suppress, or downgrade a rule.
- Prettier (`.prettierrc.json`) is the formatter, same config as `erun-ui/frontend`.
- Styling is Tailwind 4 + `erun-kit`'s design tokens (`src/styles.css` imports `erun-kit/theme.css` and declares a Tailwind `@source` pointing at `erun-kit/src`, since the kit's utility classes live under `node_modules` via the workspace symlink and are otherwise excluded from Tailwind's default content scan — see `erun-kit/AGENTS.md`). `src/styles.css` still carries console-specific layout that has no kit widget yet; convert a selector to a kit widget and delete it from here as each panel adopts one, rather than letting bespoke and shared styling coexist for the same concept (the duplicated `.status-badge` rules this repo used to carry alongside the desktop's `StatusBadge` are exactly what #1211 removed).

## Validation

Run all of these from `erun-console/` for any change to this module:

```bash
yarn install
yarn typecheck      # tsc --noEmit
yarn lint           # eslint .
yarn build          # vite build
yarn test           # vitest run — the component test
```

- `yarn test` (vitest + `@testing-library/react`, jsdom) mocks `fetch` and asserts the render/controller behaviour: `ConfigView`'s read model and empty states, the provisioning and MCP panels, and the PKCE mechanics. `vite.config.ts` scopes it to `src/`, so it never collects `playwright/`'s spec.
- The **real-IdP** verification lives in `playwright/` (its own package, own validation commands): a browser sign-in against a live Zitadel v4 through to the rendered tenant. Run it from `erun-console/playwright/` with `yarn test`; see `playwright/AGENTS.md`. Anything that changes `src/auth/` or `App.tsx`'s auth lifecycle should be re-verified there, not only under vitest.

## Running against a real erun-backend-api (dev)

The console can be driven against a **live** `erun-backend-api` with no live IdP, the same way the MCP edge is tested (issue #674): the API trusts a `file://` desktop key, so a desktop-signed token authenticates the read view end to end. For a run with a live issuer instead, use `playwright/run.sh`, which wires the same API to a real Zitadel.

- `vite.config.ts` proxies `/v1/...` to `VITE_API_PROXY_TARGET` (default `http://127.0.0.1:17033`) so `yarn dev` fetches **same-origin** — the API sets no CORS headers by design, and the dev proxy forwards server-side, so there is no browser preflight. In production the console is served same-origin behind the API edge, so no proxy is needed.
- `VITE_DEV_BEARER_TOKEN` is the bearer token `App.tsx` presents. For a real-API run it must be a token the API trusts: either an OIDC JWT, or a desktop-signed `file://` token (EdDSA, audience `erun-api`) when the API is started with `ERUN_API_DESKTOP_PUBLIC_KEY_PATH` pointing at the matching public key. See [`api-protocol.md` § Sign-in](../erun-docs/docs/agent-reference/api-protocol.md) for the token model.

```bash
# point yarn dev at a running API on :17055 with a desktop-signed token
VITE_API_PROXY_TARGET=http://127.0.0.1:17055 VITE_DEV_BEARER_TOKEN=<token> yarn dev
```

Unlike `yarn test` (mocked `fetch`), this renders the **real** `{ tenant, environments[], contexts[] }` the API serves from Postgres — the verification that closed the option-2 console↔API check for #606/#658.
