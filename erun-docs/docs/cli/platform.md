---
title: erun platform
---

# `erun platform`

Talk to a hosted erun platform's own control-plane API (`erun-backend-api`) directly — the same API the [hosted console](/collaboration/overview) drives — using the **erun-type cloud alias** [`erun cloud init erun`](/cli/cloud) and `erun cloud login` set up. It exists so an Operator or Agent can exercise or smoke-test a deployed control plane without a browser-obtained token: registering tenants and users, listing and managing hosted environments, bootstrapping or reusing cloud contexts, and previewing a full provisioning plan before running it.

For the concepts behind tenants, environments, and cloud contexts, see [Hosted platform](/concepts/hosted-platform). For day-to-day creation of a hosted environment, see [Managing hosted environments](/collaboration/hosted-environments).

## Synopsis

```
erun platform whoami [flags]
erun platform tenant create --name <name> --issuer <issuer> [flags]
erun platform tenant list [flags]
erun platform user enroll --username <username> [flags]
erun platform user list [flags]
erun platform env list [flags]
erun platform env get ENVIRONMENT_ID [flags]
erun platform env register --name <name> --type <type> [flags]
erun platform env deploy ENVIRONMENT_ID [flags]
erun platform env stop ENVIRONMENT_ID [flags]
erun platform env delete ENVIRONMENT_ID [flags]
erun platform context create --name <name> --alias <alias> --region <region> [flags]
erun platform context list [flags]
erun platform context get CONTEXT_ID [flags]
erun platform provision --env-name <name> --env-type <type> [flags]
```

Every subcommand accepts `--erun-alias` (defaults to the sole configured erun-type alias when only one is set up), `--dry-run` (trace the resolved HTTP call without sending it), and the global `--output json` for structured results.

## Subcommands

### `platform whoami`

Resolves the caller's identity against the platform: tenant ID, user ID, username, and roles.

### `platform tenant create` / `platform tenant list`

Registers a new tenant, or lists tenants visible to the caller. `create` requires the caller to be signed in as an **operations tenant** — it maps a new OIDC issuer to the tenant and is a real, immediate write. `list` returns every tenant for an operations-tenant caller, or just the caller's own tenant otherwise.

| Flag | Description |
|---|---|
| `--name` | Tenant name (hyphen-free; forms the `<tenant>-<env>` namespace). |
| `--type` | `COMPANY` (default) or `OPERATIONS`. |
| `--issuer` | OIDC issuer that resolves tokens to this tenant. |
| `--org-field-key` / `--org-field-value` | Set only for a shared (org-scoped) issuer — see [tenant issuers](/agent-reference/api-protocol#tenant-issuers). |
| `--display-name` | Label for the tenant/issuer mapping (defaults to the issuer). |

### `platform user enroll` / `platform user list`

Enrolls a user in a tenant, or lists a tenant's users. `--issuer`/`--subject` link the external identity the user signs in with; `--tenant-id` targets another tenant and is honored only for an operations-tenant caller.

`--role-id` (repeatable) names the roles the enrollment grants, instead of the platform's own default (`TenantUser`, or `TenantAdmin` for a tenant's first user). Use it to enroll a tenant's administrator directly: an enrollment that lands as an ordinary member has to be elevated from *inside* the tenant afterwards, and if no one there can grant roles yet, nothing can. List the target tenant's role ids with `GET /v1/roles` ([roles endpoints](/agent-reference/api-protocol#roles-endpoints)).

### `platform env list` / `platform env get`

Lists the caller's tenant's hosted environments, or fetches one by id.

### `platform env register`

Registers a hosted environment. For a `runtime` environment with `--runtime-version` set and a deploy executor configured on the platform, this also starts a server-side deploy — poll `platform env get` to watch its status move `registered` → `provisioning` → `running`/`failed`.

| Flag | Description |
|---|---|
| `--name` | Environment name (DNS-1123 label; forms the `<tenant>-<env>` namespace). |
| `--type` | `runtime`, `remote-agent`, or `local-agent`. |
| `--context-id` / `--kubernetes-context` | For a `runtime` environment, `--context-id` places the deploy on that registered [cloud context](/concepts/hosted-platform#single-cluster-placement) (validated to belong to your tenant, with room); omit both to auto-select one of your own registered contexts, falling back to the platform's own cluster if you have none. `--kubernetes-context` (a raw name, not a registered context) is **not supported for a `runtime` environment** — it names no known credential to authenticate with. |
| `--runtime-version` | Published erun runtime version to deploy (runtime environments only). |

### `platform env deploy`

Starts a server-side deploy of an already-registered environment, re-deploying at `--version` or the environment's own pinned runtime version. Fails with a conflict if a deploy is already in progress.

### `platform env stop`

Scales the environment's runtime Deployment to zero — the server-side equivalent of [`erun stop`](/cli/stop). Persistent state is untouched.

### `platform env delete`

Starts tearing down the environment's namespace (if it has one) and removing it — the server-side equivalent of [`erun delete`](/cli/delete). Not recoverable. Prompts for confirmation unless `-y`/`--yes` is set or `--dry-run` is used.

The teardown runs in the background: the command returns as soon as the platform accepts the delete and prints the resulting environment line, at status `deleting`:

```
  - prod (018f4b2a-...) type=runtime status=deleting
```

Poll `platform env get` to watch it converge — either to not-found (gone) or to `deletion-blocked`, in which case the same line carries a `delete-error="..."` field naming the stuck namespace's own conditions. Re-running `platform env delete` against a `deleting` or `deletion-blocked` environment retries it; the platform also re-attempts a stuck delete on its own every few minutes. `--output json` returns the environment as a structured object instead.

### `platform context create` / `platform context list` / `platform context get`

Manages the platform's own cloud contexts — the tenant's bootstrapped clusters. `create` without `--preview` launches a real cloud VM and provisions k3s on it, billing the tenant's cloud account until stopped; `--preview` asks the platform to resolve and return the bootstrap plan without creating anything (a real API call, distinct from `--dry-run`, which never reaches the network).

| Flag | Description |
|---|---|
| `--name` | Kubernetes context name to create. |
| `--alias` | Cloud provider alias (on the tenant's own account) to bootstrap with. |
| `--region`, `--instance-type`, `--disk-type`, `--disk-size-gb` | Instance shape for the context's VM. |
| `--preview` | Resolve and return the bootstrap plan without creating anything. |

### `platform provision`

Previews the full ordered plan for provisioning a hosted environment — tenant, quota, context, namespace, registration, and (for a runtime environment) deploy — without executing any of it or writing to the database. Pass either `--kubernetes-context` to reuse an existing context by raw name, or `--context-name`/`--context-alias`/`--context-region` to bootstrap a new one; either is refused for a `runtime` environment, which can only ever preview the platform's own cluster here. This preview does not yet cover placing a `runtime` environment onto an already-registered `--context-id` — that decision is made live by `env register` (no CLI preview surface for it yet); see [Placement](/concepts/hosted-platform#single-cluster-placement) for the full decision it makes.

## Examples

```bash
erun cloud init erun --api-url https://api.erunpaas.com
erun cloud login --alias erun+api.erunpaas.com@erun

erun platform whoami
erun platform env register --name prod --type runtime --runtime-version 1.4.2
erun platform env get 018f4b2a-...
erun platform env deploy 018f4b2a-... --version 1.5.0
erun platform env stop 018f4b2a-...
erun platform env delete 018f4b2a-... -y
erun platform env get 018f4b2a-...            # watch the delete converge

erun platform provision --env-name staging --env-type runtime --dry-run
erun platform tenant list --output json
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| No erun-type cloud alias configured. | Aborts before any network call, naming `erun cloud init erun --api-url <url>`. |
| More than one erun-type alias configured, `--erun-alias` omitted. | Aborts asking for an explicit `--erun-alias`. |
| `tenant create`/`user enroll --tenant-id <other>`/`user list --tenant-id <other>` by a non-operations caller. | `403 Forbidden`. |
| `env register` names `--kubernetes-context` (a raw name, not a registered context) for a `runtime` environment, or `--context-id` that does not resolve for your tenant. | `400 Bad Request`; see [Placement](/concepts/hosted-platform#single-cluster-placement). |
| `env register` names a `--context-id` that is already at its `maxEnvironments`, or names none while every one of your tenant's own registered contexts is full or not yet running. | `409 Conflict`. |
| `platform provision` names `--kubernetes-context` or a bootstrap `--context-*` set for a `runtime` environment. | `400 Bad Request` — this preview does not support `runtime` placement onto a context; see [Placement](/concepts/hosted-platform#single-cluster-placement). |
| `env deploy` while a deploy is already in progress. | `409 Conflict`. |
| `env delete` while a delete is already in progress for that environment. | `409 Conflict`. A `deletion-blocked` environment is always retryable and never conflicts. |
| `env get`/`env deploy`/`env stop`/`env delete` on an unknown environment id. | `404 Not Found`. |
| `env stop`/`env delete`/`context create`/`env register` (with a version) when the platform has no deploy/lifecycle executor configured. | `501 Not Implemented`. |
| `env delete` without `-y`/`--yes` and not `--dry-run`. | Interactive confirmation prompt; declining aborts with no request sent. |
| Environment/tenant quota reached, or admitting/redeploying a runtime environment would exceed the tenant's aggregate CPU/memory/storage budget. | `409 Conflict` on a real `env register`/`env deploy`; a preview (`platform provision`) instead returns the full plan with `quotaOk: false`. An environment you have already asked to delete does not count toward the environment-count cap. |
