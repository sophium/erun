---
title: erun orchestrator
---

# `erun orchestrator`

Manage host-side AI orchestrator definitions from the terminal — the config-file counterpart to the desktop's Edit orchestrator dialog (see [Desktop app · Orchestrators](/desktop/orchestrators)), so `config.yaml` is not the only way to change one.

## `erun orchestrator set-role`

Set what an orchestrator uses one of its already-linked environments for: a **code** environment writes code and iterates fast; a **build** environment checks out pushed branches, runs the gates, and cuts releases; a **runtime** environment is operated directly — deploy, pin, observe — with no worktree to review and no in-pod agent to delegate to, which is the only role — including undeclared — a runtime-type environment may take, while a **host** environment takes any role except that one, having no pod for those to act on. The requested role is checked against the linked environment's actual type every time, the same check the desktop's link/edit dialog applies, so this command refuses exactly the pairings that dialog would. See [`erun list`](/cli/list) for the same value read back, and [Agent reference · Skills spec](/agent-reference/skills-spec#erun-orchestrate) for how the orchestrator itself uses the role.

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
| The requested role isn't allowed for the linked environment's type (e.g. `code` or `none` against a runtime-type environment). | Aborts with `orchestrator "<id>": <tenant>/<environment> is a "<type>" environment, so it cannot take role "<role>" -- <reason>`, naming the escape hatch (`runtime` is the only role a runtime-type environment may take); exit code 1; nothing is written. A config written before this check existed can still carry an invalid pairing on disk — it still loads and lists fine, and setting a legal role clears it. |

## `erun orchestrator set-alias`

Set the erun platform alias an orchestrator declares as its own — the orchestrator-scoped counterpart of a tenant's own cloud alias ([`tenantconfig.primarycloudprovideralias`](/reference/configuration#erunconfig)), and what the desktop's Edit orchestrator dialog calls **Platform alias**. The alias you name must already be configured on this host: `erun cloud init erun --api-url <url>` adds one, and the Cloud Providers section of [`erun list`](/cli/list) shows the aliases this machine has. Naming one selects among aliases the host already holds; it does not create a credential or sign in to one. `erun list` prints the recorded value back as `platform alias:`. **This command only records the declaration** — see [Configuration · `ERunConfig`](/reference/configuration#erunconfig) for what the field does today, which is nothing beyond being validated and read back.

The value is checked against this host's configured providers before anything is written, so this refuses an alias it cannot resolve rather than storing one that would not resolve later — the same refusal `erun platform --erun-alias` gives for an unknown alias. Pass `none` to declare no alias of its own again, which is the default. An alias and `none` behave identically at run time today: an orchestrator's platform calls resolve this machine's own alias either way.

### Synopsis

```
erun orchestrator set-alias ORCHESTRATOR_ID --alias <alias|none> [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--alias` | Required. A configured erun-type cloud provider alias, or `none` to declare none of its own. |
| `--dry-run` | Resolve and trace the write without making it. |

### Examples

```bash
erun orchestrator set-alias my-orchestrator --alias erun+erunpaas.com@erun
erun orchestrator set-alias my-orchestrator --alias none
```

### Error behaviour

| Failure | Behaviour |
|---|---|
| `--alias` missing. | Cobra-required; aborts before the command runs. |
| `--alias` is empty. | Aborts with `invalid alias "": must be a configured erun platform alias, or "none" to declare none`; exit code 1; nothing is written. |
| This host has no alias by that name. | Aborts with ``orchestrator <id>: cloud provider alias "<alias>" is not configured -- name an erun platform alias this host has configured (`erun cloud init erun --api-url <url>` adds one), or pass "none" to declare none``; exit code 1; nothing is written. |
| The alias exists but is not an erun-type one (an AWS or Cloudflare alias, say). | Aborts with `orchestrator <id>: cloud provider alias "<alias>" is a "<provider>"-type alias, not an erun platform alias -- …`, same remedy; exit code 1; nothing is written. An orchestrator's platform attribution has no meaning against another provider's alias. |
| The orchestrator id doesn't exist. | Aborts with `orchestrator "<id>" not found`; exit code 1. |
| The id is omitted. | Aborts with ``set orchestrator alias: no orchestrator id given — pass one explicitly (`erun orchestrator set-alias <id> --alias <alias>`)``; exit code 1. |
