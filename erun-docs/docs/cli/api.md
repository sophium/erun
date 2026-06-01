---
title: erun api
---

# `erun api`

Run ERun's backend API server over HTTP. This is an infrastructure command — it's wired into the hosted backend deployment, not something you run day to day. It launches the `eapi` binary; the CLI itself doesn't host the API.

## Synopsis

```
erun api [TENANT] [ENVIRONMENT] [flags]
```

## Flags

| Flag | Description |
|---|---|
| `--host` | Interface to bind (default `127.0.0.1`). |
| `--port` | Port to bind (default `17033`, else the environment's allocated API port). |
| `--database-url` | Backend PostgreSQL URL (defaults to `ERUN_DATABASE_URL`). |
| `--oidc-allowed-issuers` | Comma-separated OIDC issuer allow-list. |
| `--aws-identity-store-id`, `--aws-identity-store-region` | AWS Identity Center lookup for resolving usernames from STS tokens. |

The flag set describes what the API does: PostgreSQL-backed, OIDC-authenticated, with AWS Identity Center username resolution. The protocol is specified in [Agent reference · erun API](/agent-reference/api-protocol).

## Examples

```bash
erun api --host 127.0.0.1 --port 17033
erun api my-tenant dev
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| `eapi` binary not found. | Errors with "build or install it first". |
| Invalid port. | Rejected before binding. |
