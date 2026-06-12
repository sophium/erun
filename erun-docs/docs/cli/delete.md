---
title: erun delete
---

# `erun delete`

Delete an environment from your ERun configuration and remove its remote runtime namespace.

## Synopsis

```
erun delete TENANT ENVIRONMENT [flags]
```

Both arguments are required. `erun delete` is destructive — there is no `delete` of the parent tenant via this command. To remove a tenant entirely, delete each of its environments first.

## What it removes

1. The Kubernetes namespace `<tenant>-<env>` and everything inside it (the runtime pod, dind daemon, MCP container, PVCs for `/home/erun` and `/var/lib/docker`, helm releases, etc.).
2. The local environment config (`~/.config/erun/<tenant>/<env>/`).
3. The tenant's `default_environment` pointer if it referenced the deleted env — a sibling env (if any) is promoted to default.

The cached port-forward state for the env (`<UserConfigDir>/erun/portforward/...`) is left in place; a later env with the same name overwrites it.

The tenant config itself (`~/.config/erun/<tenant>/tenant.yaml`) and the project's `.erun/config.yaml` are **not** removed.

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Show every action that would be performed without executing. Strongly recommended to run first. |

## Examples

```bash
erun delete my-tenant rihards-dev --dry-run     # preview
erun delete my-tenant rihards-dev               # actually delete
```

## When to use it vs `kubectl delete namespace`

Prefer `erun delete`:

- It tears down both local and remote state in one step (otherwise your local config will reference a namespace that no longer exists).
- It updates the default-environment pointer correctly.
- It cleans up cached port-forward files so the desktop app doesn't try to connect to a vanished port.

Use `kubectl delete namespace <tenant>-<env>` only when you've already lost the local config and want to clean up the remote side manually.

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant + env not configured. | Errors with "no such environment"; nothing is touched. |
| Cluster unreachable. | Aborts before deleting any local state. The remote namespace (if it exists) is left intact. |
| Namespace already gone but local config exists. | Proceeds — removes the local config and port-forward state, reports the namespace as already absent. |
| Helm uninstall fails for one of the releases. | Continues with namespace delete (the `kubectl delete namespace` reclaims any leftover resources); logs the helm error. |
| User declines interactive confirmation. | Exits 0 with "cancelled"; no side effect. Use `--dry-run` to preview without prompting. |

`erun delete` is a destructive operation — `--dry-run` is strongly recommended for first-time use against an unfamiliar env.
