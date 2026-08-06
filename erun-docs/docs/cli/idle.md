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

The resolved idle policy (timeout, working hours, timezone), whether the environment is cloud-managed, whether it's currently eligible to stop and why not if it isn't, the per-marker activity breakdown (ssh, api, mcp, cli, codex, process, lease), any activity leases currently held, and any armed auto-stop grace window.

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

The `--json` shape (policy, markers, activity, leases, pending-stop fields) is specified in [Agent reference · Idle policy](/agent-reference/idle-policy).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant + environment not resolvable. | Errors; nothing is read. |
| No activity recorded yet. | Reports markers as idle with no last-activity timestamp — not an error. |

## Activity leases {#activity-leases}

A request-shaped signal cannot describe long work: a detached build or agent run makes no calls between its first second and its last, so an environment under continuous heavy use reads as untouched. An **activity lease** is how that work announces itself. While a lease is held the environment reports as busy with the lease's name, the desktop's sidebar says so, and idle-stop leaves the environment alone.

Take one around anything long:

```bash
erun activity lease take --tenant my-tenant --environment prod --name gradle-build --pid $$
trap 'erun activity lease release --tenant my-tenant --environment prod --id gradle-build' EXIT
./gradlew build
```

```bash
erun activity lease list --tenant my-tenant --environment prod
erun activity lease release --tenant my-tenant --environment prod --id gradle-build
```

An Agent working over MCP takes the same lease through the `activity_lease_take` and `activity_lease_release` tools.

A lease cannot pin an environment awake forever. It expires unless renewed (default 15 minutes, `--ttl`), it is capped at a hard maximum lifetime however often it is renewed, and one whose `--pid` holder has exited is reclaimed the next time anything reads the leases. Taking the same id again renews it, so a wrapper can refresh on a timer without tracking whether it already holds one.

Work that takes no lease is still noticed: the in-pod monitor samples the container's resident build and agent processes every 30 seconds and records activity for the ones burning CPU. That is a backstop, not a substitute — it can only say "something is working", where a lease says what.

The full lease schema, the expiry and liveness rules, and the MCP tool shapes are specified in [Agent reference · Idle policy](/agent-reference/idle-policy#activity-leases).

### Error behaviour

| Failure | Behaviour |
|---|---|
| `take` without `--name`. | Aborts with `lease name is required`; exit code 1; nothing is written. |
| `take`, `release`, or `list` without a tenant + environment. | Aborts with `tenant and environment are required`; exit code 1. |
| `release` of an unknown or already-expired lease. | Succeeds; exit code 0 — so a wrapper's exit trap never fails a job that finished cleanly. |
| A lease file that cannot be read. | Dropped on the next read rather than blocking idle-stop on an unreadable claim. |
