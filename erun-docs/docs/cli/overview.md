---
title: CLI overview
---

# CLI overview

The `erun` CLI is the primary interface to ERun. Every command supports `--help` and `--dry-run`.

## Commands

| Command | What it does |
|---|---|
| `erun init <tenant> <env>` | Create or update a tenant + environment, deploy runtime pod. |
| `erun open <tenant> <env>` | Open a shell into the runtime pod (deploys it if missing). |
| `erun list` | List tenants, environments, status, and effective target. |
| `erun build` | Build the project's Docker images (snapshot for local, `./build.sh` for non-local). |
| `erun push` | Push built images to the configured container registry. |
| `erun deploy` | Build → push → roll out the helm chart. |
| `erun doctor` | Inspect the local config or the runtime pod for problems. |
| `erun mcp` | Launch the MCP server (used by IDEs and AI tooling). |
| `erun version` | Print build version + commit. |

## Verbosity and dry-run

```
--verbose / -v       Stream external tool output (helm --debug, kubectl --v=4, …)
-vv                  Above, plus per-command trace lines
--dry-run            Resolve and print everything that would run; perform no side effects
--time               Print elapsed wall time
```

Every action-oriented command supports `--dry-run` — try it first when you're not sure what a command will do.

## Detailed reference

(Detailed per-command pages will live under this section.)
