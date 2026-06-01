---
title: erun idle
---

# `erun idle`

Show an environment's idle and auto-stop status. Read-only — it reports what the idle monitor sees and would do, and **records no activity itself** (so checking status never keeps an environment alive).

## Synopsis

```
erun idle [TENANT] [ENVIRONMENT] [flags]
```

## What it shows

The resolved idle policy (timeout, working hours, timezone), whether the environment is cloud-managed, whether it's currently eligible to stop and why not if it isn't, the per-marker activity breakdown (ssh, api, mcp, cli, codex), and any armed auto-stop grace window.

## Flags

| Flag | Description |
|---|---|
| `--json` | Emit the full status as JSON. |
| `--tenant`, `--environment` | Target a specific tenant/environment. |

## Examples

```bash
erun idle my-tenant prod
erun idle my-tenant prod --json
```

The `--json` shape (policy, markers, activity, pending-stop fields) is specified in [Agent reference · Idle policy](/agent-reference/idle-policy).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant + environment not resolvable. | Errors; nothing is read. |
| No activity recorded yet. | Reports markers as idle with no last-activity timestamp — not an error. |
