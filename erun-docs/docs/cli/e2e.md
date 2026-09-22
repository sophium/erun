---
title: erun e2e
---

# `erun e2e`

Discover the project's `playwright/` folder the way [`erun build`](/cli/build) discovers `docker/` and [`erun deploy`](/cli/deploy) discovers `k8s/`, then run it once against a real, already-deployed environment. See [Delivery pipeline · What `e2e` does](/pipeline#what-e2e-does) for where this sits in the flow.

## Synopsis

```
erun e2e [--tenant <t>] [--environment <e>] [--component <name>] [flags]
```

## What it does

1. Discovers the project's e2e suite: `paths.playwright` (or a selected `components.<name>.playwright` entry) when configured, else the `<tenant>-devops/playwright` convention default. A project with no `playwright/` folder at all is a clean no-op — exit `0`, nothing runs.
2. Refuses before a browser starts, naming the cause, when the environment isn't deployed, the target service isn't exposed, or its certificate isn't issued yet.
3. Refuses a discovered suite that sets `ignoreHTTPSErrors` or hardcodes a literal `baseURL` — both would silently defeat the guarantee below.
4. Runs the suite once, injecting two values the suite never declares itself:
   - `ERUN_E2E_BASE_URL` — the environment's resolved, real HTTPS URL (the same hostname `erun expose` published), with a certificate that is actually verified.
   - `ERUN_E2E_VERSION` — the version the environment is currently deployed at, read from the live helm release.

The suite's first assertion is normally that the served surface reports `ERUN_E2E_VERSION` — that's what turns a green run into real proof the deployment landed, rather than a pass that would look identical against a stale rollout.

```bash
erun e2e --tenant my-tenant --environment dev
erun e2e --tenant my-tenant --environment dev --component my-service
```

`erun e2e` is never triggered by `erun deploy` — it's a separate step with its own exit code. The everyday shortcut for a branch is `erun build --e2e`, which composes build → push → deploy → e2e in one command (see [`erun build`](/cli/build)).

## What the suite owns

Authentication, fixtures, and seeding — ERun cannot log into a tenant's own application and does not try to. `erun e2e` guarantees the URL, the certificate, and the version; everything the suite asserts beyond that is yours.

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target a specific tenant/environment; default to the current scope. |
| `--component <name>` | Select one component's suite when `playwright/` holds more than one. Auto-selects the lone suite when only one is discovered. |
| `--dry-run` | Resolve the suite, the base URL, and the deployed version, and trace every precondition check, without starting Playwright. |

## From an MCP-connected orchestrator

The same run reaches an Agent through the `e2e` MCP tool, including `preview` (dry-run) and background-job support (`wait: false`) for a suite that takes a while — see [MCP overview](/mcp/overview).

## Error behaviour

| Failure | Behaviour |
|---|---|
| No `playwright/` folder resolves. | Clean no-op; exit `0`. |
| More than one per-component suite and no `--component`. | Errors naming every discovered component. |
| `--component <name>` names a suite that doesn't exist. | Errors naming the requested component. |
| The suite sets `ignoreHTTPSErrors`, or hardcodes a literal `baseURL`. | Errors naming the file and the specific violation, before any environment check runs. |
| The environment isn't deployed. | Errors naming the tenant/environment and the remedy (`erun deploy <tenant> <environment>`). |
| The target service isn't exposed. | Errors naming the service and the remedy (`erun expose <service>`). |
| The service is exposed but its certificate isn't ready yet. | Errors naming the hostname and cert-manager's own reason. |
| Playwright itself fails. | Exits non-zero with Playwright's own output; the environment is left deployed and running — a failed run is not rolled back, since the point of leaving it up is to go look at it. |
