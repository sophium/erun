---
title: erun list
---

# `erun list`

List all configured tenants and environments, the effective target, and managed cloud contexts. `list` is read-only and never mutates state.

## Synopsis

```
erun list [flags]
```

## Output

The default output is a tree:

```
config directory: /Users/you/Library/Application Support/erun
default tenant: my-tenant

current directory:
  effective target: my-tenant / local

tenants:
  - my-tenant [default, effective]
    default environment: local
    environments:
      - local [default, effective]
        kubernetes context: docker-desktop
        container-registry: ghcr.io/sophium
        runtime version: 1.0.76
      - rihards-dev
        kubernetes context: erun-004-020362606330-eu-west-2
        ...
```

The full per-env field set (local port allocations, API URL, repo path, …) is listed; the example above abbreviates. See [Configuration](/reference/configuration) for what each per-env value means.

## Common usages

```bash
erun list                            # full tree
erun list | grep -E "(tenant|env)"   # quick scan of names
erun list | grep container-registry  # see configured registries per env
```

`erun list` is what every troubleshooting flow should start with — it tells you which environment ERun considers "effective" right now and what its resolved config looks like.

## Subcommands

| Subcommand | Description |
|---|---|
| `erun list cloud` | List managed ERun cloud contexts only. |

### `erun list cloud` output

A flat list of every cloud context configured on the current user — alias, provider, cluster id, region, instance type, and the current [lifecycle status](/concepts/cloud-contexts#lifecycle):

```
cloud contexts:
  - MyOrg+020362606330@aws [running]
    provider: aws (alias: MyOrg)
    cluster:  erun-004-020362606330-eu-west-2
    region:   eu-west-2
    instance: t3.large
    bound envs: my-tenant / rihards-dev, my-tenant / claude-review

  - MyOrg+020362606330@aws-staging [stopped]
    provider: aws (alias: MyOrg)
    cluster:  erun-005-020362606330-eu-west-2
    region:   eu-west-2
    instance: t3.medium
    bound envs: (none)
```

Useful for spotting cloud contexts that are running idle, or for figuring out which env(s) a context backs before stopping it.
