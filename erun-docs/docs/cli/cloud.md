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
```

## Subcommands

### `cloud init aws`

Registers an AWS IAM Identity Center (SSO) profile and saves it as a cloud provider alias. It writes an SSO profile to `~/.aws/config`, **opens an AWS SSO login in your browser**, resolves your identity, enables outbound web-identity federation, derives the OIDC issuer the deployed ERun APIs will trust, and stores the alias.

| Flag | Description |
|---|---|
| `--sso-start-url` | AWS IAM Identity Center start URL. |
| `--sso-region` | Identity Center region. |
| `--account-id` | AWS account ID to log into. |
| `--role-name` | AWS role to assume. |
| `--region` | Default region for the alias (defaults to `--sso-region`). |
| `--oidc-issuer-url` | Override the OIDC issuer (normally derived from the live token). |

Omitted flags are prompted for interactively.

### `cloud init cloudflare`

Saves a delegated, account-scoped Cloudflare API token as a cloud provider alias. Run with **no flags** for a guided, step-by-step setup:

1. **Create the token** — ERun prints the Cloudflare token page and the exact permissions to set (**account-level `Zone:Edit` + `DNS:Edit`**), then waits while you mint it in your already-logged-in browser session.
2. **Paste the token** — entered masked and verified against the Cloudflare API; an invalid token re-prompts in place.
3. **Confirm the account** — ERun resolves the account ID from the token automatically (a picker appears if the token sees more than one account), and proposes an editable token label.

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

## Examples

```bash
erun cloud init aws --dry-run     # preview the AWS calls and writes
erun cloud init cloudflare        # guided: create token, paste, auto-resolve account
erun cloud init cloudflare --account-id 0a1b2c3d --token-name dns-edit --api-token <token>   # non-interactive
erun cloud login --alias me+123456789012@aws
erun cloud set my-tenant prod --alias me+123456789012@aws
erun cloud set my-tenant prod --alias dns-edit+0a1b2c3d@cloudflare   # coexists with the AWS alias
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

AWS and Cloudflare are supported today; any other provider is rejected before side effects.
