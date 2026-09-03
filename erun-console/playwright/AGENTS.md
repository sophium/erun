# AGENTS.md

Module-specific guidance for `erun-console/playwright` — the console's real-infrastructure end-to-end suites: OIDC sign-in against a real Zitadel, driving a real live MCP edge (JSON-RPC and WebSocket attach), and real-backend REST write surfaces. Follow the repository root `AGENTS.md` and `erun-console/AGENTS.md` first, then apply this file for work in this subtree.

## Role

This package holds every console verification that needs something `erun-console`'s own `yarn test` (vitest, mocked `fetch`/`WebSocket`) cannot give it — a real browser against a real backing service:

- **`tests/oidc-signin.spec.ts`** is the **real-IdP** verification for the console's OIDC Authorization Code + PKCE sign-in: the **whole flow through an actual browser against a real Zitadel v4**, which is the only place that proves the redirect, the Login V2 round-trips, the PKCE code exchange, the API's JWKS signature verification, and the first-sign-in tenant bootstrap all fit together. A mock IdP here would prove nothing the unit tests do not already.
- **`tests/mcp-operate-scope.spec.ts`** is the **real-edge** verification for the console's per-env MCP JSON-RPC access surface (`src/mcp/liveClient.ts`, `OperateToolForm`): a real browser, at a different origin than a real `emcp` instance, minting an `erun:operate`-scoped token and driving its tools against the real edge — the one thing a mocked `fetch` (`MCPAccessPanel.test.tsx`) cannot prove, because the whole point is what a real cross-origin round trip does. See "The MCP operate-scope e2e" below.
- **`tests/mcp-attach-session.spec.ts`** is the same real-edge verification for the sibling WebSocket attach surface (`src/mcp/attachClient.ts`, `AttachSessionForm`): a real browser dialing a real `emcp` instance's attach endpoint over `wss://` (a self-signed TLS front, since `attachEdgeUrl` always dials `wss://`), driving a real `dtach`/PTY session end to end. See "The WebSocket attach-session e2e" below.
- **`tests/rest-surfaces.spec.ts`** is the real-backend verification for the console's plain-REST write surfaces (`src/provision/`, `src/tenants/`) that route through the same same-origin `httpBaseQuery` transport the config read view already proves live — no separate host, no hand-rolled wire protocol, so no cross-origin risk to exercise, but still a real round trip a mocked `fetch` cannot prove matches the real API's JSON shape. See "The REST-surfaces e2e" below.

Each spec is independently opt-in (see "Gating") and stands up only the infrastructure it needs — running one never requires another's stack.

It is a **separate yarn package** from the console app (its own `package.json`, `tsconfig`, `eslint.config.mjs`), exactly like `erun-ui/playwright` is separate from `erun-ui/frontend`. The console app's `eslint .` ignores this directory, and its `tsc`/`vite`/`vitest` never see it.

## Gating

Opt-in and skipped by default, the same convention as the backend's live env-deploy gate (`ERUN_E2E_ENV_DEPLOY=1`): each spec calls `test.skip()` unless its own gate env var is set (`ERUN_E2E_CONSOLE_OIDC=1`, `ERUN_E2E_CONSOLE_MCP_OPERATE=1`, `ERUN_E2E_CONSOLE_MCP_ATTACH=1`, `ERUN_E2E_CONSOLE_REST=1`), and each spec's own `run*.sh` script is the only thing that sets its gate — after it has actually stood the relevant stack up. A suite that silently assumes a running dependency is worse than no suite, so a bare `yarn playwright test` skips all of them rather than failing against nothing.

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
yarn install                        # once
yarn install-browsers                # once — Playwright Chromium
yarn test                            # run.sh: OIDC sign-in against a real Zitadel
yarn test --headed                   # watch the browser
yarn test:mcp-operate-scope          # run-mcp-operate-scope.sh: real Postgres + eapi + emcp + console
yarn test:mcp-operate-scope:headed
yarn test:mcp-attach-session         # run-mcp-attach-session.sh: same, but the WebSocket attach edge
yarn test:mcp-attach-session:headed
yarn test:rest-surfaces              # run-rest-surfaces.sh: real Postgres + eapi + console, no emcp
yarn test:rest-surfaces:headed
```

Prerequisites on PATH: `docker`, `go`, `atlas`, `yarn`, `openssl`, `python3` (`node` too, for `mcp-attach-session`'s self-signed TLS front). Each script uses its own disjoint port range so any two can run back-to-back with no teardown race, and each refuses to start when one of its own ports is already taken rather than testing against whatever is there: `run.sh` needs 8080/5173/17055/5544; `run-mcp-operate-scope.sh` needs 5545/17057/5175/28100; `run-mcp-attach-session.sh` needs 5547/17059/5177/28150/28151; `run-rest-surfaces.sh` needs 5548/17060/5178.

## Conventions

- `retries: 0`, `workers: 1`, `fullyParallel: false` — a sign-in (or a tool call) that only passes on a retry is a determinism defect to fix, never to mask. Wait on observable conditions (URLs, visible elements, HTTP status), never wall-clock sleeps.
- **Background services are killed by process group.** `$!` on a subshell is the subshell, not the server it starts; a dev server or container that survives teardown will serve the _next_ run the _previous_ run's client id (or bearer token), and the failure looks like a broken redirect rather than a leak. `setsid` + `kill -- -$PGID`, plus the free-port precondition, is what keeps that from recurring.
- Pin the Zitadel image (`v4.15.3`) — core and `zitadel-login` must be the **same** tag.
- Keep each suite a thin driver: assert on what the operator sees (the Login V2 pages then the rendered tenant; the MCP panel's own rendered text; the provision/tenant panels' own status text), not on internal endpoints.

## The MCP operate-scope e2e

`run-mcp-operate-scope.sh` proves the property erun#1107 Phase 3 / erun#763's `erun:operate` tier exists for (extended to the console by erun#2024/#2026/#2035): a console session minted at `erun:operate` can drive the operate-shaped tools over a **real** live MCP edge, and is refused every admin-only tool the tier must keep it away from. It needs no Zitadel — the console signs in via the desktop-signed dev-token flow (`erun-console/AGENTS.md`'s "Running against a real erun-backend-api (dev)"), minted by hand with `openssl pkeyutl -sign` (Ed25519/EdDSA is a PureEdDSA scheme: it signs the JWT signing input directly, no separate digest step) rather than a bespoke Go tool, since this suite otherwise has no Go module of its own.

- **What it stands up:** a real Postgres, a real `erun-backend-api` (`ERUN_API_MCP_SIGNING_KEY_PATH` configured, so `POST /v1/environments/{id}/mcp-token` really signs), and a real `emcp` binary — the same one a deployed runtime pod runs — inside a throwaway **rootful** Docker container. The container is the one part that needs root: `emcp`'s file-issuer verifier reads its trusted public key from a fixed path, `/etc/erun/mcp-auth/desktopid.pub` (`eruncommon.DesktopMCPPublicKeyPath`), which this pod's own unprivileged user cannot write; a container gets its own filesystem and its own root for free.
- **Who signs in:** deliberately an ordinary `TenantUser`, created with only that role (`POST /v1/users` with an explicit `roleIds`), never the bootstrap identity that becomes the tenant's own admin. `erun:operate` exists for a caller with no delete-environment entitlement — signing in as the admin instead would prove nothing about the tier this suite exists to check.
- **What the spec proves, and how:** `OperateToolForm`'s `context_start` and `deploy` calls reach real business logic (a domain error, not a capability refusal) — proving the tier's own capability check passes. Then, from inside the same browser tab, using the token the UI just minted, it calls `exec_raw`/`delete`/`terraform`/`init` over the exact wire protocol `liveClient.ts` speaks and asserts each comes back `"unknown tool \"<name>\""` — the edge never registers an admin-only tool for a non-admin capability set at all (`erun-mcp/capabilities.go`), so there is nothing in the console's own UI that could ever reach one (the Tool dropdown's four options are asserted directly). A second test proves the console names a clear, actionable reason (not a bare "Forbidden") when this same `TenantUser` picks "Admin" in the scope selector with no delete-environment entitlement.
- **What running it for real found:** the MCP go-sdk (v1.4.1+) installs Go's stdlib `net/http.CrossOriginProtection` by default, which 403'd every cross-origin tool call independently of `corsMiddleware`'s CORS headers — meaning this console feature had never actually worked from a real browser at a different origin than the edge until this suite's first run caught it and `erun-mcp/server.go` was fixed. See `erun-console/AGENTS.md` and `erun-mcp/AGENTS.md` for the fix.

## The WebSocket attach-session e2e

`run-mcp-attach-session.sh` proves the browser-side half of erun#1692's attach edge (`src/mcp/attachClient.ts`, `MCPAccessPanel.tsx`'s `AttachSessionForm`) against a real `emcp` instance — the one thing `attachClient.test.ts`'s mocked `WebSocket` cannot prove, and the same class of gap `mcp-operate-scope.spec.ts` found a real defect in for the sibling JSON-RPC edge.

- **What it stands up, beyond the operate-scope harness's shape:** the throwaway `emcp` container installs `dtach` (the operate-scope harness's edge never needs it — it never drives a real session), and a plain Node `tls`/`net` TCP proxy (embedded in the script, no new repo file) terminates a self-signed cert in front of the container's plain-HTTP port. `attachEdgeUrl` always dials `wss://` regardless of what scheme a caller passes in — correct for a real deployed edge (always behind the platform's TLS ingress, with the console itself served over `https`, so a plain `ws://` would be mixed-content-blocked) but it means this harness cannot point the browser straight at the container's plain-HTTP port the way the JSON-RPC spec's shortcut does. The spec's own browser context accepts the self-signed cert explicitly (`test.use({ ignoreHTTPSErrors: true })`) — the same trust decision an operator's browser makes for a real CA-issued cert, just not one Chromium extends by default.
- **What running it for real found:** on a throwaway container with no CLI-driven session ever run in it (the ordinary state of a freshly deployed or restarted pod), the attach handshake succeeded but the takeover script failed — `eruncommon.RemoteAppSessionSocketDir` did not exist yet, and nothing in the WebSocket edge's own code path created it (only the CLI shell path did, and `session-prune.sh`'s boot-time reconciliation explicitly no-ops rather than creating it). The console rendered the raw shell error as PTY output and a misdiagnosed `"taken-over"` outcome. `erun-mcp/attach.go`'s `runAttachSession` now creates the directory itself before running the takeover script; see `erun-mcp/AGENTS.md` and `erun-mcp/attach_test.go`'s `TestAttachCreatesSessionDirectoryOnAFreshPod` for the Go-level regression test. Once fixed, the spec proves a real cross-origin browser attach: mint → connect → `echo` a marker → see it in the scrollback → disconnect.

## The REST-surfaces e2e

`run-rest-surfaces.sh` proves two of the console's plain-REST write surfaces (`src/provision/`, `src/tenants/`) against a real `erun-backend-api` — no `emcp`, no TLS front, no Zitadel: both route through the same same-origin `httpBaseQuery` transport `GET /v1/config` already proves live, so this harness is the plain Postgres+`eapi`+console shape with nothing else added.

- **What running it for real found:** `ProvisionPanel`'s alias-registration call (`PUT /v1/cloud-provider-aliases/{alias}`) 404'd outright. `erun-backend-api/server.go` only registered that route when `options.Cipher != nil` (gated on `ERUN_SECRETS_KEY`, which this harness had not set), and no chart in `erun-devops/` sets that variable for a real deployment either — so this was not a harness gap, it was every real deployment's own state. The harness now sets `ERUN_SECRETS_KEY` so the route is actually exercised; the backend fix (always register the route, refuse with a named 501 when unconfigured, matching `mintMCPToken`'s own nil-signer pattern) is in `erun-backend-api/AGENTS.md`.
- **Why the cloud context never reaches "running" in this harness:** no `ContextProvisioner` is wired (needs `options.DBOSContext`, a heavier dependency this harness deliberately does not add), so `POST /v1/contexts` only registers the row and returns `201` — the spec asserts the "polling" state a real, correctly-shaped `CloudContext` response produces, not a terminal outcome.
- **What this harness deliberately does not cover, and why:** `src/identity/`'s Zitadel-backed panels need a real Zitadel Management API (the heavier topology `zitadel/stack.sh` already stands up for `oidc-signin.spec.ts`, not yet wired to identity administration); `environmentsApi.ts`'s `deployEnvironment` needs a real job/cluster runner to reach a terminal outcome. Both are the same low-risk same-origin transport as what this harness does cover, but proving that requires standing up materially more infrastructure than this pass's scope — named here rather than silently left uncovered.
