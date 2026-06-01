---
title: erun cloud
---

# `erun cloud`

Set up and manage **cloud provider aliases** — the AWS credentials and OIDC issuer that managed [cloud contexts](/cli/context) and remote environments use. A cloud alias is named `<user>+<account>@aws` and is stored in your root ERun config.

## Synopsis

```
erun cloud init aws [flags]
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

### `cloud login`

Refreshes the AWS SSO session for an alias (`aws sso login`). Only touches the local SSO token cache. `--alias` selects the alias (prompted if omitted; **required** with `--dry-run`).

### `cloud oidc`

Re-derives and saves the OIDC issuer for an alias by minting a short-lived AWS web-identity token and reading its issuer. `--alias` selects it; `--audience` sets the token audience (default `erun-api`).

### `cloud set`

Assigns an alias to a specific environment (local config only, no network). `TENANT` and `ENVIRONMENT` are required; `--alias` names the alias to assign. For a remote environment this also marks it cloud-managed.

## Examples

```bash
erun cloud init aws --dry-run     # preview the AWS calls and writes
erun cloud login --alias me+123456789012@aws
erun cloud set my-tenant prod --alias me+123456789012@aws
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Required SSO field missing (non-interactive). | Aborts before any AWS call or file write. |
| SSO login / token mint fails. | Surfaces the AWS error; no alias is saved. |
| Alias not configured (`login`/`oidc`/`set`). | Errors with "cloud provider alias … is not configured". |
| `--dry-run` without `--alias` on `login`. | Errors asking for an explicit `--alias`. |

Only AWS is supported today; any other provider is rejected before side effects.
