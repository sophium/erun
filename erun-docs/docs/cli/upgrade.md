---
title: erun upgrade
---

# `erun upgrade`

Redeploy every environment opted into **Upgrade all** to the latest version for its release channel. `erun upgrade` is the one-command way to roll a fleet of environments forward without running `erun deploy` for each — it resolves the latest version per channel, then redeploys only the environments whose current version lags.

## Synopsis

```
erun upgrade [TENANT] [ENVIRONMENT] [flags]
```

With no arguments it considers every opted-in environment across all tenants. Pass `TENANT` (or `TENANT ENVIRONMENT`) to narrow the scope.

## What "opted in" means

An environment joins the Upgrade-all set when it turns on **Include in Upgrade all** (its `autoupgrade` setting), and it declares a **channel** — `stable` (semver releases) or `snapshot` (latest snapshot build). Both are set per environment in the desktop's environment settings, or directly in config (see [Configuration](/reference/configuration#per-project-config)). When the channel is unset, it defaults from the environment type: runtime environments track `stable`, agent environments track `snapshot`.

For each opted-in environment, `erun upgrade` resolves the latest version for its channel from the runtime image registry and compares it to the environment's current version. The `snapshot` channel follows the newest build wherever it lives: a snapshot is a pre-release of the stable release that follows it, so once a stable release is published on top of the latest snapshot, the stable release becomes the channel's target instead of the older snapshot. When a newer snapshot stream starts (its base version outranks the latest stable), snapshots take over again. Environments already at the latest are left untouched; lagging ones are redeployed (the equivalent of `erun deploy <tenant> <env> --version <latest>`), which both rolls out the new image and records the new version in the environment's config.

## Flags

| Flag | Description |
|---|---|
| `--version <version>` | Deploy this exact version to every opted-in environment, skipping channel/registry resolution. |
| `--tenant <tenant>` | Restrict the upgrade to one tenant. |
| `--environment <env>` | Restrict the upgrade to one environment (requires `--tenant`). |
| `--force` | Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected. |
| `--dry-run` | Resolve and print the plan — each member, its channel, current → target — and the deploy actions, without executing. |

## High blast radius

`erun upgrade` rolls out new runtime images to **multiple, possibly remote** environments, restarting their pods and potentially spending cloud money. Run `erun upgrade --dry-run` first: it prints the resolved plan (which environments are opted in, their channels, and current → target) and the exact deploy actions for each lagging member before anything ships. In the desktop app, the **Upgrade all** button shows the same plan in a confirmation dialog before any deploy.

## Examples

Preview the plan for every opted-in environment:

```bash
erun upgrade --dry-run
```

Upgrade every opted-in environment to its channel's latest:

```bash
erun upgrade
```

Upgrade only one tenant's opted-in environments:

```bash
erun upgrade team
```

Pin one environment to an exact version:

```bash
erun upgrade team prod --version 1.2.3
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| No environments opted in (in scope). | Prints "No environments opted into Upgrade all" and exits 0 — nothing to do. |
| Tenant image repo can't be listed (private registry, never published). | The channel target falls back to the canonical `erun-devops` image — the base the tenant's wrapper image is rebuilt from at the requested version — so the environment still upgrades. A listable tenant repo refines the target (its published stream wins per channel). |
| Channel latest can't be resolved anywhere (the canonical image lookup failed too, or no matching tags). | The environment is reported as **target unresolved** with the reason, in the plan line, the skip trace, and the completion accounting (`==> Upgrade complete: N upgraded, N up to date, N unresolved, N failed`) — never as "up to date", and `erun upgrade` never deploys an unknown version. |
| `--environment` without `--tenant`. | Errors before any work; exit code 1. |
| One member's deploy fails. | The run **continues** to the remaining members; the failed environment is reported in a summary and the command exits non-zero. Already-upgraded members stay deployed. |
| Cluster unreachable / stopped cloud context for a member. | Surfaces per the underlying `erun deploy` behaviour for that member (see [`erun deploy` · Error behaviour](/cli/deploy#error-behaviour)). |

`erun upgrade --dry-run` prints the full plan and the per-member deploy actions ahead of time, so the Operator can audit exactly what would ship before committing.
