# AGENTS.md

Hosted-console guidance. Follow root `AGENTS.md` and shared frontend/UX guidance
in `erun-kit/AGENTS.md`. Real-infrastructure tests also read `playwright/AGENTS.md`.

## Module role

- This is the hosted React SPA, separate from the Wails desktop. Call
  `erun-backend-api` directly over HTTP; do not introduce a BFF or import desktop
  code. Reuse components and wire models through kit.
- Keep brand, API addresses, and OIDC configuration instance-configurable;
  example production domains must not become source literals.
- The console owns its navigation shell and HTTP/state wiring. Shared domain
  resolution remains behind the API, not reimplemented in the browser.

## What is verifiable here vs. what is a flagged placeholder

Keep verification claims tied to actual test boundaries:

| Surface                        | Evidence                                             | Remaining boundary                                                 |
| ------------------------------ | ---------------------------------------------------- | ------------------------------------------------------------------ |
| Config and app panels          | Vitest component tests with mocked fetch             | Mock success alone is not live API proof                           |
| OIDC Authorization Code + PKCE | Auth unit tests and real-Zitadel browser sign-in     | Dev bearer-token mode is not OIDC verification                     |
| MCP operate scope              | Real cross-origin browser + emcp as TenantUser       | Public services-zone ingress is not exercised by the local edge    |
| WebSocket attach               | Real browser + TLS front + dtach/PTY on a fresh edge | Full terminal emulation is not implemented; the view is line-based |
| Alias/context/tenant writes    | Real REST suite                                      | Context registration is tested, not terminal cloud provisioning    |

- Preserve hostname discovery from `exposedHostname`, editable override, and a
  labelled manual fallback when not exposed.
- Discover session IDs from the existing ai-sessions endpoint. Autofill a live
  candidate, not exited/oom-killed sessions; keep deliberate quick-pick/manual
  entry available and explain an empty discovery result.
- Keep credentials server-side: browsers mint scoped per-environment tokens,
  never hold the signing key. Operate-scope proof must demonstrate allowed
  business logic and refusal of admin-only tools, not only successful token mint.
- Cross-origin request handling and fresh attach-directory initialization are
  MCP-owned contracts; see `erun-mcp/AGENTS.md`, not duplicated incident histories.
- Disclosed live gaps: public services-zone ingress; identity administration
  against the real Management API; terminal environment deployment with a real
  job/cluster runner. An architecture argument is not verification.
- Cloud alias writes need configured backend encryption. The unconfigured route
  returns an actionable 501, not a missing-route 404; provisioning
  `ERUN_SECRETS_KEY` in deployed charts remains a separate deployment requirement.

## Layout

- `src/shell/`: persistent sidebar/header and one derived active section;
  branded pre-auth screens. Keep navigation and app composition console-local.
- Tenant switching re-authenticates with `prompt=select_account`; it is never
  client-side tenant re-scoping. Consume the one-shot switch intent after config
  resolves and show a mismatch if the API-selected tenant differs.
- `src/app/`: one `platformApi` with injected domain endpoints over
  `httpBaseQuery`, auth lifecycle in thunks, and a `createAppStore` factory.
  Mutations invalidate the appropriate shared tags, not manual refresh callbacks.
- `src/config/`: render kit's shared config model and endpoint contract.
  Pre-token platform discovery remains a plain async request.
- `src/provision/`: adapt alias/context mutations and polling to the panel;
  distinguish registration, running, and failed outcomes.
- `src/mcp/`: own token lifecycle, JSON-RPC client, and WebSocket client.
  Attach mints `erun:attach`; operate controls map to operate tools, not the
  admin-only version smoke test. Preserve structured backend failure messages.
- `src/identity/` and `src/tenants/`: operations-tenant administration.
  Keep IdP Management API credentials exclusively in the backend. Invalidate
  identity/config tags after writes and associate validation errors with fields.
- `src/auth/`: real OIDC/PKCE plus explicit local-dev token fallback, not a
  placeholder. `authThunks` owns auth resolution and `App.tsx` renders distinct
  loading, signed-out, not-enrolled, error, and ready states.
- Tests use `renderWithStore` with a fresh store per render; never leak RTK Query
  caches between cases. `playwright/` is an independently configured package.

## Permission degradation

Apply kit's capability-based, three-empty-state contract. Tenant-type eligibility
for operations-only panels remains separate from permissions. Finer-grained
capability gating is a disclosed implementation gap: when needed, put the shared
shape in kit and app mapping in its owner, not a component-local role-name check.

## Toolchain

- Yarn, Vite, React, strict TypeScript, Redux Toolkit/RTK Query, ESLint, and Prettier.
  Checked-in package versions and configs are authoritative; no alternate lockfiles.
- Use kit widgets/primitives and semantic Tailwind tokens. Include kit sources
  via Tailwind `@source`; workspace symlinks do not ensure automatic scanning.
  Keep app CSS for true globals. Theme is the shared `.dark` class contract.

## Professional UX

Apply kit's Professional UX, UX Impact Review Checklist, and Design-Language
Decision Record. Record checklist answers in the PR. The test item means component
tests for ordinary rendered flows and the relevant real-infrastructure Playwright
suite for auth, MCP, or live API boundaries; neither silently substitutes for the other.

## Validation

- Code changes: `yarn install`, then `yarn typecheck && yarn lint &&
yarn format:check && yarn build && yarn test` here.
- Vitest collects `src/`, not the independent Playwright package. Test setup
  supplies jsdom's missing ResizeObserver/matchMedia behavior.
- Auth lifecycle changes run the real-Zitadel browser suite. Changes crossing an
  MCP or REST boundary run the corresponding suite under `playwright/AGENTS.md`.
- Guidance-only changes follow root validation scope.

## Running against a real erun-backend-api (dev)

- The Vite proxy sends `/v1` to `VITE_API_PROXY_TARGET` (default
  `http://127.0.0.1:17033`). Keep API calls same-origin; the API deliberately
  does not supply browser CORS. Production routes through the same-origin edge.
- `VITE_DEV_BEARER_TOKEN` must be a trusted token, not a fabricated identity.
  A desktop-signed file-issuer token uses EdDSA, audience `erun-api`, and a matching
  `ERUN_API_DESKTOP_PUBLIC_KEY_PATH` on the API. OIDC uses the real issuer flow.
  See `erun-docs/docs/agent-reference/api-protocol.md` for the token contract.

## Can The Console Host Orchestrators? (#1692, design recorded — not yet implemented)

Manual MCP calls and attach are implemented; an unattended hosted orchestrator
is not. Preserve these unresolved requirements rather than implying that browser
attach or detached jobs supply a hosted orchestrator:

- Refreshable, non-human credentials are required after the browser's access token
  expires. Reuse the backend agent-credential design (#1969); never persist an
  operator's browser token as workload identity. Shared tenant machine identity
  also raises unresolved per-orchestrator/operator audit-attribution questions.
- Requests inherit one tenant from authenticated security context. Unlike desktop
  orchestrators, a console session cannot span linked environments across tenants
  without a separately authorized identity design.
- Delegated environment jobs already survive client disconnects. The orchestrator's
  own persistent workload still needs a durable record, scheduling, cost, and
  idle/lifetime policy. Existing dtach attach can reconnect to such a workload
  once one exists; it does not provision or own it.
- Diff review can use `exec_diff`; artifact delivery can use outputs tools.
  No host review-directory/mirror abstraction is required for either.
- A browser cannot execute a downloaded host-native artifact. Explicitly decline
  that verification step; never report cross-building/downloading as a native run.
- Blocking decisions: agent credentials, workload placement/cost and multiplicity,
  and audit attribution. Cross-tenant narrowing and inability to run native
  artifacts must be explicit in any delivered design.
