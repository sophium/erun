---
name: erun-browser-session-rest
description: Make authenticated REST calls to a host whose org blocks API tokens and admin-gates OAuth, by reusing a saved browser login session (Playwright storageState). Use when the user says "authenticated REST via a browser session", "call an API that blocks API tokens", "reuse my browser login for API calls", "hit the <host> API without a token", or describes an enterprise lockdown where personal access tokens are disabled and OAuth apps need admin approval.
---

# Authenticated REST via a saved browser session

Some enterprise hosts disable personal API tokens **and** gate OAuth-app
creation behind admin approval, leaving no first-class way to call their REST
API from a script. This skill bridges that gap: you log in once through a real
browser (SSO + MFA and all), save the resulting session, and make authenticated
REST calls that reuse it — no token, no stored password.

## When to use

Reach for this **only when proper auth is genuinely unavailable** — API tokens
are disabled and OAuth is admin-gated. A token or an approved OAuth app is
always the better path; this is the fallback when the org has closed those off.
Trigger phrasings: "authenticated REST via a browser session", "call an API
that blocks API tokens", "reuse my browser login for API calls", "hit the
`<host>` API without a token".

This skill is **host-agnostic**: it bakes in no host, credentials, or IdP. You
supply the host and the login URL; the SSO flow is whatever your browser does.

## Prerequisites

- Node 18+ (native `fetch`/ESM) and Playwright with a browser:
  `npx playwright install chromium` (one-time).
- The two helpers ship beside this file: `save-session.mjs` (interactive login →
  saved session) and `request.mjs` (authenticated call, session rolled forward).

## 1. Establish and save a session

Run the login helper with the host's login URL. A real browser window opens;
log in there (including MFA), then press Enter to save. Your password is never
read or stored — only the resulting session cookies are saved.

```sh
ERUN_REST_SESSION=./session.json \
  node save-session.mjs --login https://your-host.example/login
```

## 2. Make authenticated calls

Point the request helper at the host and call any endpoint. The session is
**rolled forward** (re-saved) after each call so refreshed cookies persist.

```sh
ERUN_REST_BASE_URL=https://your-host.example ERUN_REST_SESSION=./session.json \
  node request.mjs GET /rest/api/2/myself
# with a body:
ERUN_REST_BASE_URL=https://your-host.example \
  node request.mjs POST /rest/api/2/issue '{"fields":{"summary":"hi"}}'
```

`request.mjs` prints the response body to stdout and exits non-zero on an HTTP
error (with the status on stderr), so it composes in pipelines and `jq`.

## Security caveats

- **`session.json` is a live credential.** It carries active session cookies —
  treat it like a password. Keep it out of git (`echo session.json >>
  .gitignore`), off shared storage, and delete it when done. In a deployed ERun
  env, write it under `${ERUN_OUTPUTS_DIR}` only if you intend to pull it out;
  otherwise keep it on the pod's ephemeral disk.
- **Sessions expire.** When calls start returning 401/redirect-to-login, re-run
  step 1. The session is short-lived by design — that is the host's policy, not
  a bug.
- **SSO-DOM automation is brittle.** This skill keeps the login *manual* (you
  drive the browser) precisely because scripting an SSO login against a
  changing DOM is fragile and easy to get wrong. Do not extend `save-session.mjs`
  to type credentials unless you accept that brittleness and never hard-code a
  password.
- This is a **fallback**. If the org later enables API tokens or approves an
  OAuth app, switch to that — it is auditable and revocable per-credential.
