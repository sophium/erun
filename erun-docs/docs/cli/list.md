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
  path: /Users/you/code/my-project
  repo: my-project
  configured tenant: my-tenant
  effective target: my-tenant / local
    kubernetes context: docker-desktop
    repo path: /Users/you/code/my-project
    api url: http://127.0.0.1:17033

tenants:
  - my-tenant [default, effective]
    default environment: local
    environments:
      - local [default, effective]
        kubernetes context: docker-desktop
        container-registry: ghcr.io/sophium
        runtime version: 1.0.76
        local ports: mcp=17000 api=17033 ssh=17022
      - rihards-dev
        kubernetes context: erun-004-020362606330-eu-west-2
        container-registry: 020362606330.dkr.ecr.eu-west-2.amazonaws.com
        ...
```

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
