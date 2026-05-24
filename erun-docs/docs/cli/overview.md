---
title: CLI overview
---

# CLI overview

The `erun` CLI is the primary interface to ERun. Every command supports `--help` and `--dry-run`.

## Commands

| Command | What it does |
|---|---|
| [`erun init`](/cli/init) | Create or update a tenant + environment, bring the environment up. |
| [`erun open`](/cli/open) | Open a shell into the environment (brings it up if missing). |
| [`erun list`](/cli/list) | List tenants, environments, status, and effective target. |
| [`erun build`](/cli/build) | Build the project's Docker images (snapshot for local, `./build.sh` for non-local). |
| [`erun push`](/cli/push) | Push built images to the configured container registry. |
| [`erun deploy`](/cli/deploy) | Build → push → roll out the helm chart. |
| [`erun doctor`](/cli/doctor) | Inspect the local config or a running environment for problems. |
| [`erun mcp`](/cli/mcp) | Launch the MCP server (used by IDEs and AI tooling). |
| [`erun release`](/cli/release) | Plan and execute a project release. |
| [`erun delete`](/cli/delete) | Remove an environment's namespace and local config. |
| [`erun version`](/cli/version) | Print build version + commit. |

## Verbosity and dry-run

```
--verbose / -v       Stream external tool output (helm --debug, kubectl --v=4, …)
-vv                  Above, plus per-command trace lines for every action and decision
--dry-run            Resolve and print everything that would run; perform no side effects.
                     Implies trace verbosity.
--time               Print elapsed wall time after the command finishes.
```

Every action-oriented command supports `--dry-run` — try it first when you're not sure what a command will do. The trace lines you see in `--dry-run` mode are the same lines ERun would emit during a real run, so dry-run output is a faithful preview.

## How arguments resolve

Most commands accept `[TENANT] [ENVIRONMENT]` as positional args. When omitted, ERun resolves them in this order:

1. Explicit `--tenant` / `--environment` flags, if present.
2. The current working directory's git repository — matched against each tenant's configured `project_root`.
3. The default tenant (`~/.config/erun/config.yaml` → `default_tenant`) and the tenant's `default_environment`.
4. Interactive prompt, when running in a TTY.
5. Error, in non-interactive contexts.
