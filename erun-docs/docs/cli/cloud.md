---
title: erun cloud
---

# `erun cloud`

Set up and manage **cloud provider aliases** — the cloud credentials that managed [cloud contexts](/cli/context) and remote environments use. AWS aliases carry an IAM Identity Center profile and the OIDC issuer the deployed ERun APIs trust; Cloudflare aliases carry a delegated, account-scoped API token. An AWS alias is named `<user>+<account>@aws`; a Cloudflare alias is named `<token-name>+<account>@cloudflare`. Aliases are stored in your root ERun config — except the Cloudflare token itself, which is held in a local secret store referenced from config (never written into `erun-config.yaml`).

## Synopsis

```
erun cloud init aws [flags]
erun cloud init cloudflare [flags]
erun cloud login [flags]
erun cloud oidc [flags]
erun cloud set TENANT ENVIRONMENT --alias <alias>
erun cloud refresh TENANT ENVIRONMENT
```

## Subcommands

### `cloud init aws`

Registers an AWS IAM Identity Center (SSO) profile and saves it as a cloud provider alias. It writes an SSO profile to `~/.aws/config`, **opens an AWS SSO login in your browser**, resolves your identity, enables outbound web-identity federation, derives the OIDC issuer the deployed ERun APIs will trust, and stores the alias.

| Flag | Description |
|---|---|
| `--sso-start-url` | AWS IAM Identity Center start URL. |
| `--sso-region` | Identity Center region. |
| `--account-id` | AWS account ID to log into. |
| `--role-name` | AWS permission set (IAM Identity Center) to assume — the name shown under the account in your AWS access portal. |
| `--region` | Default region for the alias (defaults to `--sso-region`). |
| `--oidc-issuer-url` | Override the OIDC issuer (normally derived from the live token). |

Omitted flags are prompted for interactively.

### `cloud init cloudflare`

Saves a delegated, account-scoped Cloudflare API token as a cloud provider alias. Run with **no flags** for a guided, step-by-step setup:

1. **Create the token, then paste it** — ERun prints the Cloudflare token page and the **Custom Token** permissions to set — `Zone → Zone → Edit` and `Zone → DNS → Edit` (Zone Resources `Include → All zones`), plus `Account → Cloudflare Pages → Edit` and any other scopes you'll use (Account Resources `Include → your account`) — then prompts for the token right there. Mint it in your already-logged-in browser session and paste it into the same prompt; it's entered masked and verified against the Cloudflare API, and an invalid token re-prompts in place.
2. **Confirm the account** — ERun resolves the account ID from the token automatically (a picker appears if the token sees more than one account), and proposes an editable token label.

The token is stored in a **local secret store** referenced from your config (never written into `erun-config.yaml`), and the alias is named `<token-name>+<account>@cloudflare`. There is no browser login and no OIDC issuer — Cloudflare authenticates with the token directly.

| Flag | Description |
|---|---|
| `--api-token` | The scoped API token value. Providing it runs non-interactively (for scripts/MCP); omit it for the guided flow above. Never echoed or written to config. |
| `--account-id` | Cloudflare account ID. Optional in the guided flow (auto-resolved from the token); required when `--api-token` is supplied. |
| `--token-name` | Label for the scoped API token (becomes part of the alias). Defaults in the guided flow; required when `--api-token` is supplied. |

### `cloud login`

Refreshes the credential for an alias. For an AWS alias this runs `aws sso login` and only touches the local SSO token cache; for a Cloudflare alias it re-verifies the stored token against the Cloudflare API. `--alias` selects the alias (prompted if omitted; **required** with `--dry-run`).

### `cloud oidc`

Re-derives and saves the OIDC issuer for an **AWS** alias by minting a short-lived AWS web-identity token and reading its issuer. `--alias` selects it; `--audience` sets the token audience (default `erun-api`). Cloudflare aliases have no OIDC issuer, so this command rejects them.

### `cloud set`

Assigns an alias to a specific environment (local config only, no network). `TENANT` and `ENVIRONMENT` are required; `--alias` names the alias to assign. The command routes the alias by its provider type, so an environment can carry **one AWS alias and one Cloudflare alias at the same time** — assigning a Cloudflare alias does not displace an AWS one. For a remote environment this also marks it cloud-managed.

### `cloud refresh`

Refreshes the environment's in-pod copy of your AWS credentials. Attaching an AWS alias to an environment makes its runtime pod act as your AWS identity through the `erun-host` profile in the pod's `~/.aws/credentials` (see [Acting as your AWS identity](/deployment/cloud-setup#host-credentials)). Those credentials are **temporary** — roughly an hour — so a long-running environment eventually loses AWS access. This command mints a fresh set from the profile behind the alias and rewrites that one profile in place, leaving every other profile in the file untouched.

Nothing secret passes through the caller. ERun reads your profile itself and streams the credentials to the pod on **stdin**, so no key, secret, or session token appears in a command argument, a trace line, or an Agent transcript. That makes this the verb to use from scripts and Agents — and the reason to prefer it over the `cloud_inject_aws_credentials` MCP tool, which takes the values as tool arguments and is therefore only safe for the desktop's in-memory refresher.

`erun open` runs the same refresh, so in day-to-day use you rarely call this directly; reach for it when a session that has been open for hours starts failing AWS calls, or from automation that never reopens the environment. `TENANT` and `ENVIRONMENT` are both required. It needs an unexpired local SSO session — run `erun cloud login --alias <alias>` first if yours has lapsed, because SSO needs a browser and this command cannot open one.

The region ERun writes into the profile is resolved in this order: the environment's managed [cloud context](/cli/context), then the region encoded in its kubeconfig context name, then the alias's Identity Center region, then the region in an ECR registry host. If none resolves, the profile carries no region and the command says so — see [`doctor`](/cli/doctor) and the [region troubleshooting entry](/reference/troubleshooting#aws-region-empty).

## Examples

```bash
erun cloud init aws --dry-run     # preview the AWS calls and writes
erun cloud init cloudflare        # guided: create token, paste, auto-resolve account
erun cloud init cloudflare --account-id 0a1b2c3d --token-name dns-edit --api-token <token>   # non-interactive
erun cloud login --alias me+123456789012@aws
erun cloud set my-tenant prod --alias me+123456789012@aws
erun cloud set my-tenant prod --alias dns-edit+0a1b2c3d@cloudflare   # coexists with the AWS alias
erun cloud refresh my-tenant prod             # re-inject host credentials into the runtime pod
erun cloud refresh my-tenant prod --dry-run   # show the plan, touch nothing
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Required SSO field missing (non-interactive). | Aborts before any AWS call or file write. |
| SSO login / token mint fails. | Surfaces the AWS error; no alias is saved. |
| Cloudflare account ID, token name, or token missing (non-interactive). | Aborts before contacting Cloudflare; no alias is saved. |
| Cloudflare rejects the token (`init`/`login`). | Surfaces the Cloudflare error; the alias is not saved (init) and the token reads as expired (login). |
| `cloud oidc` against a Cloudflare alias. | Errors that the alias is Cloudflare and does not use OIDC web-identity federation. |
| Alias not configured (`login`/`oidc`/`set`). | Errors with "cloud provider alias … is not configured". |
| `--dry-run` without `--alias` on `login`. | Errors asking for an explicit `--alias`. |
| `cloud refresh` against an environment with no AWS alias. | Aborts before contacting the cluster; exit code 1; names the `erun cloud set <tenant> <env> --alias <alias>` that would fix it. |
| `cloud refresh` against a Cloudflare alias. | Errors that host credential refresh applies to AWS aliases only. A Cloudflare alias ships its token as a chart Secret at deploy time instead — nothing to refresh. |
| `cloud refresh` with a lapsed SSO session. | The credential export fails; exit code 1; the error names `erun cloud login --alias <alias>`. The pod keeps whatever credentials it already had. |
| `cloud refresh` when the runtime is not up. | The deployment wait fails; exit code 1; nothing is written to the pod. |

AWS and Cloudflare are supported today; any other provider is rejected before side effects.
