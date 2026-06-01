---
title: erun list
---

# `erun list`

List all configured tenants and environments, the effective target for the current directory, and configured cloud providers. `list` is read-only and never mutates state. (To list managed cloud *contexts*, use [`erun context list`](/cli/context).)

## Synopsis

```
erun list [flags]
```

## Output

Sections print in order — configuration location, defaults, the effective target for the current directory, configured cloud providers, then every tenant and its environments:

```
Configuration:
  directory: /Users/you/Library/Application Support/erun
Defaults:
  tenant: my-tenant
  environment: local
Current Directory:
  path: /Users/you/code/my-project
  repo: my-project
  configured tenant: my-tenant
  effective target: my-tenant/local
  kubernetes context: docker-desktop
  type: local-agent
  snapshot: enabled
  repo path: /Users/you/code/my-project
Cloud Providers:
  none
Tenants:
  - my-tenant (default)
    ...
```

The full per-env field set (local port allocations, API URL, SSH details, …) prints under each tenant; the example abbreviates. See [Configuration](/reference/configuration) for what each value means.

## Common usages

```bash
erun list                         # full listing
erun list | grep -i "tenant"      # quick scan of names
erun list | grep "effective"      # what ERun targets right now
```

`erun list` is what every troubleshooting flow should start with — it tells you which environment ERun considers "effective" right now and what its resolved config looks like.

## Error behaviour

| Failure | Behaviour |
|---|---|
| No config yet. | Prints the sections with `none` placeholders; not an error. |
| Current directory isn't a configured project. | `effective target: none` (or `unavailable (…)` with the reason); the rest still prints. |
| Config file unreadable. | Errors with the read failure; nothing is printed. |
