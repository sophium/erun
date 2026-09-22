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
2. The local environment config (`<config-root>/<tenant>/<env>/`).
3. The tenant's `default_environment` pointer if it referenced the deleted env — a sibling env (if any) is promoted to default.
4. The env's port-forward record under `<UserConfigDir>/erun/portforward/...`: the state file, the log, and the log's rotated generation. A log a forward that is still running holds open is kept.
5. The `Host erun-<tenant>-<env>` block from your `~/.ssh/config`, when no other configured environment resolves to that alias — only that block is removed, and the rest of the file is untouched.

Items 4 and 5 name state that would otherwise outlive the environment and point at local ports the environment no longer holds. Deleting an environment frees its port range for whichever environment is created next, so a surviving record does not fail — it silently resolves to a different, live environment. `ssh erun-<tenant>-<env>` after a delete reports the alias as unknown instead of connecting somewhere you did not name. Run `erun sshd init` again to recreate the alias if you kept a copy of the environment elsewhere.

A sibling environment that still derives the same alias keeps its block; because the alias is sanitized from the tenant and environment names, two names that differ only in punctuation (for example `dev-1` and `dev_1`) share one alias, and deleting either leaves the block in place.

The tenant config itself (`<config-root>/<tenant>/config.yaml`) and the project's `.erun/config.yaml` are **not** removed.

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
- It cleans up the cached port-forward files and the `~/.ssh/config` alias, so neither the desktop app nor an ssh client resolves the deleted environment's local port to whichever environment inherits it.

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
