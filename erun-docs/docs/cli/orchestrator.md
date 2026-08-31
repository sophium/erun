---
title: erun orchestrator
---

# `erun orchestrator`

Manage host-side AI orchestrator definitions from the terminal — the config-file counterpart to the desktop's Edit orchestrator dialog (see [Desktop app · Orchestrators](/desktop/orchestrators)), so `config.yaml` is not the only way to change one.

## `erun orchestrator set-role`

Set what an orchestrator uses one of its already-linked environments for: a **code** environment writes code and iterates fast; a **build** environment checks out pushed branches, runs the gates, and cuts releases; a **runtime** environment is operated directly — deploy, pin, observe — with no worktree to review and no in-pod agent to delegate to, which is the only role a runtime-type environment may take. See [`erun list`](/cli/list) for the same value read back, and [Agent reference · Skills spec](/agent-reference/skills-spec#erun-orchestrate) for how the orchestrator itself uses the role.

### Synopsis

```
erun orchestrator set-role ORCHESTRATOR_ID TENANT ENVIRONMENT --role <code|build|runtime|none> [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--role` | Required. `code`, `build`, `runtime`, or `none` to declare the role undeclared again. |
| `--dry-run` | Resolve and trace the write without making it. |

### Examples

```bash
erun orchestrator set-role my-orchestrator my-tenant prod --role build
erun orchestrator set-role my-orchestrator my-tenant prod --role none
```

### Error behaviour

| Failure | Behaviour |
|---|---|
| `--role` missing. | Cobra-required; aborts before the command runs. |
| `--role` is not `code`, `build`, `runtime`, or `none`. | Aborts with `invalid role "<value>": must be "code", "build", "runtime", or "none" (undeclared)`; exit code 1; nothing is written. |
| The orchestrator id doesn't exist. | Aborts with `orchestrator "<id>" not found`; exit code 1. |
| The environment isn't linked to that orchestrator. | Aborts with `orchestrator "<id>" is not linked to <tenant>/<environment>`; exit code 1. Link it first — from the desktop dialog, or by hand-editing `config.yaml`. |
