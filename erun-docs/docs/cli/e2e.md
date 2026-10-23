---
title: erun e2e
---

# `erun e2e`

Run the project's end-to-end suites against a real, already-deployed environment. Each suite is a Dockerfile under `<tenant>-devops/e2e/<component>/`, discovered the way [`erun build`](/cli/build) discovers `docker/` and [`erun deploy`](/cli/deploy) discovers `k8s/`, and `erun e2e` runs each one as a `docker build` of its `e2e` stage. See [Delivery pipeline · What `e2e` does](/pipeline#what-e2e-does) for where this sits in the flow.

## Synopsis

```
erun e2e [--tenant <t>] [--environment <e>] [--component <name>] [--base-url <url>] [--version <v>] [flags]
```

## What it does

1. Discovers the suites: every subdirectory of `<tenant>-devops/e2e/` (or the configured `paths.e2e` directory) holding a `Dockerfile` with a stage named `e2e`. A project with no `e2e/` folder is a clean no-op: exit `0`, nothing runs.
2. Resolves the version the environment is deployed at, and the HTTPS URL of every exposed service.
3. Refuses before any suite runs, naming the cause, when the environment isn't deployed, a suite's service isn't exposed, its certificate isn't issued yet, or its URL points at a loopback address. It also refuses a suite whose Playwright config sets `ignoreHTTPSErrors` or hardcodes a `baseURL`: both would silently defeat the guarantee below.
4. Runs every suite in name order, or only the one `--component` names. Each run is a `docker build` of the suite's `e2e` stage that reruns the suite every time while its dependency install stays cached, and keeps no image. The suite gets values it never declares itself, as build arguments:
   - `ERUN_E2E_BASE_URL` — the real HTTPS URL of the exposed service named like the suite's directory, with a certificate that is actually verified.
   - `ERUN_E2E_URL_<SERVICE>` — the URL of every exposed service, for a suite that talks to more than one.
   - `ERUN_E2E_VERSION` — the version the environment is currently deployed at, read from the live release, or the one `--version` names.
5. Runs every suite even after one fails, then exits non-zero if any failed.

The suite's first assertion is normally that the served surface reports `ERUN_E2E_VERSION` — that's what turns a green run into real proof the deployment landed, rather than a pass that would look identical against a stale rollout.

```bash
erun e2e --tenant my-tenant --environment dev
erun e2e --tenant my-tenant --environment dev --component my-service
erun e2e --tenant my-tenant --environment dev --component my-service --base-url https://my-service.example.com
erun e2e --component my-service --base-url https://my-service.example.com --version 1.4.2
```

The suite's Dockerfile, the build command `erun e2e` runs, and every refusal are specified in [Dockerfile stages · The `e2e` stage](/agent-reference/dockerfile-stages#e2e-stage).

## A separate step, not a gate

`erun e2e` is its own command with its own exit code. `erun build`, `erun deploy`, releases and the merge queue never run it, and it is not a release or merge gate.

A failing suite does not block, roll back, or undo anything, even against a production version — the environment is left deployed and running, so you can go look at it. `erun e2e` does not file issues either. Instead, its failure output is ready to file as a bug:

```
e2e: FAILED my-service
  suite:     my-tenant-devops/e2e/my-service
  component: my-service
  target:    https://my-service.my-tenant-dev.example.com
  version:   1.4.2 (deployed to my-tenant/dev)
  step:      docker build --target e2e failed: exit status 1
  log:       ~/.erun/my-tenant/dev/e2e/my-service.log
  last lines of the log:
    ...
```

Whoever ran it — you, an Agent using the `erun-file-issue` skill, or a scheduled run — files the bug against that version.

## What the suite owns

Authentication, fixtures, and seeding — ERun cannot log into a tenant's own application and does not try to. `erun e2e` guarantees the URL, the certificate, and the version; everything the suite asserts beyond that is yours. A credential the suite needs, such as a test account's password, is declared in the project's [`e2e.secrets`](/reference/configuration#e2e-secrets) list and reaches the suite as a BuildKit secret, never as a build argument.

## Pointing a suite somewhere else

`--base-url` runs the suites against an address you name instead of the exposed service's. The exposure and certificate checks are skipped, because they are about the exposed service you are not using. Use it for an environment reached some other way, or for one whose ingress resolves to `127.0.0.1` (a local cluster), which a suite running inside `docker build` cannot reach.

The suites still need the version that address should be serving, and `erun e2e` only trusts an environment to supply it when you name that environment:

- With `--tenant`/`--environment`, the URL is taken to serve that environment, and its deployed version is resolved as usual.
- With `--version <v>`, the suites expect `<v>`. Together with `--base-url`, the URL and version name the target completely, so no environment is read at all — this is how you point the suites at a deployment this machine has no cluster access to, such as production. No tenant has to be configured on the machine, and the run's failure summary and `--output json` result name the URL (`baseUrl`) instead of a tenant and environment. Each suite's log is written to `~/.erun/e2e/<component>.log`.
- With neither, `erun e2e` refuses and asks for `--version`: the current environment's deployed version need not be what that URL serves, and comparing against it would fail a healthy deployment, or pass a stale one.

`--version` also works without `--base-url`, to have the suites expect a version other than the one the environment reports — the deployment and exposure checks still run.

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target a specific tenant/environment; default to the current scope. |
| `--component <name>` | Run only the named suite (a directory under `e2e/`). Without it, every suite runs. |
| `--base-url <url>` | Run the suites against this `http(s)` URL instead of the exposed service's, skipping the exposure and certificate checks. Needs `--version` or an explicit `--tenant`/`--environment`. |
| `--version <v>` | The version the suites expect (`ERUN_E2E_VERSION`) instead of the environment's deployed version. With `--base-url`, no environment is read. |
| `--dry-run` | Resolve the suites, URLs and deployed version, trace every check, and print the exact `docker build` commands without running them. |

## From an MCP-connected orchestrator

The same run reaches an Agent through the `e2e` MCP tool, with `component`, `baseUrl` and `version` inputs, `preview` (dry-run), and background-job support (`wait: false`) for suites that take a while — see [MCP overview](/mcp/overview).

## Error behaviour

| Failure | Behaviour |
|---|---|
| No `e2e/` folder, or one with no suites. | Clean no-op; exit `0`. |
| The project still has a `<tenant>-devops/playwright` folder, or sets `paths.playwright` / `components.<name>.playwright`. | Errors naming the move to `<tenant>-devops/e2e/<component>/Dockerfile`; nothing runs. |
| A suite's `Dockerfile` has no stage named exactly `e2e`. | Errors naming the file. |
| `--component <name>` names a suite that doesn't exist. | Errors naming the requested suite and the discovered ones. |
| The suite sets `ignoreHTTPSErrors`, or hardcodes a literal `baseURL`. | Errors naming the file and the specific violation. |
| `--base-url` without `--version` and without `--tenant`/`--environment`. | Errors asking for `--version`, or for the environment the URL serves; nothing is read from the current environment. |
| The environment isn't deployed. | Errors naming the tenant/environment and the remedy (`erun deploy <tenant> <environment>`). |
| A suite's service isn't exposed. | Errors naming the service and the remedies (`erun expose <service>`, or `--base-url`). |
| The service is exposed but its certificate isn't ready yet. | Errors naming the hostname and cert-manager's own reason. |
| A suite's URL resolves to a loopback address. | Errors explaining that a suite running in `docker build` cannot reach it, and suggests `--base-url`. |
| A declared `e2e.secrets` entry cannot be supplied. | Errors naming the entry and the missing variable or file. |
| A suite fails. | The other suites still run. Prints the bug-ready failure block above for each failed suite and exits non-zero. Nothing is rolled back, blocked, or filed. |

## Where next

- **[Dockerfile stages](/agent-reference/dockerfile-stages#e2e-stage)** — the suite Dockerfile and the exact `docker build` `erun e2e` runs.
- **[Delivery pipeline](/pipeline)** — where e2e sits after build and deploy.
- **[`erun expose`](/cli/expose)** — publish the service a suite targets.
