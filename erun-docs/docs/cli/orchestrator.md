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
| The id is omitted. | Cobra's argument check aborts before the command runs with `accepts 1 arg(s), received 0`; exit code 1. |
| The id is empty or whitespace (`""`). | Aborts with ``set orchestrator alias: no orchestrator id given — pass one explicitly (`erun orchestrator set-alias <id> --alias <alias>`)``; exit code 1; nothing is written. |

## Launch settings: gateway, model, and effort {#launch-settings}

Three settings decide how an orchestrator's Claude session launches. The desktop's Edit orchestrator dialog calls them **Use gateway**, **Model**, and **Effort** (see [Desktop app · Orchestrators](/desktop/orchestrators)); the three commands below write the same fields from the terminal, and [`erun list`](/cli/list) prints each one back once it is set. An orchestrator with none of them set launches exactly as every orchestrator did before they existed: on this machine's own Claude Code configuration, on `opus`, at `ultracode` effort.

**These commands only write `config.yaml`.** A running orchestrator keeps the session it launched with; the desktop marks it "Restart to apply" until you restart it, and a stopped orchestrator picks the new values up on its next start. Each value is validated against the gateway catalog when you write it, and validated again when the orchestrator starts: a catalog edited after the write can make a recorded value unlaunchable, and the orchestrator then refuses to start with the same reason these commands give, rather than launching something other than what you recorded.

The three settings are validated together, as the orchestrator would be after the write, not one field at a time. Turning the gateway on can make an already-recorded model undriveable, so whichever command would create that combination is the one that refuses it.

## `erun orchestrator set-gateway`

Route an orchestrator's session through the erun-level gateway catalog — the [`openrouter`](/reference/configuration#erunconfig) block the desktop's ERun settings maintain, the same catalog your environments use — or keep it on this machine's own Claude Code configuration. Off is the default. This is an opt-in per orchestrator: an orchestrator does not inherit the gateway because the catalog exists, and turning it on does not change any environment.

Through the gateway, the session's model defaults to the catalog's default model (falling back to `opus` when the catalog lists none), and the session receives the gateway's base URL and credential in its process environment — see [Environment variables · Orchestrator-session variables](/reference/env-vars#orchestrator-session-variables). The credential is resolved when the orchestrator starts, the way `erun deploy` resolves it for an environment: the key saved in the desktop's ERun settings, or this machine's own Claude Code key when nothing is saved and that key already belongs to the same gateway. When neither resolves, the orchestrator refuses to start, naming which case it hit, and the remedy is to save the gateway key in the desktop's ERun settings.

Off means off only for what erun sets. Claude Code itself applies the `env` block of this machine's own Claude Code settings to every session it starts, so a gateway configured there still applies to an orchestrator whose gateway is off — erun does not override or strip it.

### Synopsis

```
erun orchestrator set-gateway ORCHESTRATOR_ID --gateway <on|off> [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--gateway` | Required. `on` to route the session through the gateway catalog, `off` to keep it on this machine's own Claude Code configuration. |
| `--dry-run` | Validate and trace the write (`write orchestrator <id> gateway`) without making it. |

### Examples

```bash
erun orchestrator set-gateway my-orchestrator --gateway on
erun orchestrator set-gateway my-orchestrator --gateway off --dry-run
```

### Error behaviour

| Failure | Behaviour |
|---|---|
| `--gateway` missing. | Cobra-required; aborts before the command runs. |
| `--gateway` is not `on` or `off`. | Aborts with `invalid gateway "<value>": must be "on" or "off"`; exit code 1; nothing is written. |
| `on` with no gateway catalog configured. | Aborts with ``orchestrator <id>: the gateway is turned on but no gateway catalog is configured -- configure the gateway catalog in the desktop's ERun settings, or turn the gateway off (`erun orchestrator set-gateway <id> --gateway off`)``; exit code 1; nothing is written. A catalog needs at least a base URL. |
| `on` while the orchestrator's recorded model is one the gateway cannot drive. | Aborts with ``orchestrator <id>: model "<model>" cannot be driven through the gateway: its provider requires its own reasoning echoed back -- choose another model (`erun orchestrator set-model <id> --model <model>`), or keep the gateway off``; exit code 1; nothing is written. |
| The orchestrator id doesn't exist. | Aborts with `orchestrator "<id>" not found`; exit code 1. |
| The id is omitted. | Cobra's argument check aborts before the command runs with `accepts 1 arg(s), received 0`; exit code 1. |
| The id is empty or whitespace (`""`). | Aborts with ``set orchestrator gateway: no orchestrator id given — pass one explicitly (`erun orchestrator set-gateway <id> --gateway <on\|off>`)``; exit code 1; nothing is written. |

## `erun orchestrator set-model`

Set the model an orchestrator's session launches on, passed to Claude as `--model` and mirrored to the session's subagents. Pass `default` to clear it: off the gateway the default is `opus`; through the gateway it is the catalog's default model, or `opus` when the catalog lists none.

A model id is an opaque token to erun — resolving it to a concrete model is Claude's or the gateway's concern — so any id is accepted that is made of letters, digits, and `.` `_` `:` `/` `-` and does not start with `-`. When the orchestrator uses the gateway, a model the gateway cannot drive is refused: one whose provider requires its own reasoning echoed back on every request, which Claude Code never sends. Beyond that, a model does not have to be listed in the catalog, and nothing checks that the provider on the other side serves it: a model recorded for this machine's own configuration stays recorded when you turn the gateway on, so check it still names something the gateway serves.

### Synopsis

```
erun orchestrator set-model ORCHESTRATOR_ID --model <model|default> [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--model` | Required. A model id, or `default` to clear it back to the default. |
| `--dry-run` | Validate and trace the write (`write orchestrator <id> model`) without making it. |

### Examples

```bash
erun orchestrator set-model my-orchestrator --model sonnet
erun orchestrator set-model my-orchestrator --model default
```

### Error behaviour

| Failure | Behaviour |
|---|---|
| `--model` missing. | Cobra-required; aborts before the command runs. |
| `--model` is empty. | Aborts with `invalid model "": must be a model id, or "default" to clear it`; exit code 1; nothing is written. |
| The model is not a safe model id (a character outside letters, digits, `.` `_` `:` `/` `-`, or a leading `-`). | Aborts with ``orchestrator <id>: invalid model "<model>": a model id is letters, digits and . _ : / -, and cannot start with - -- name another model or clear it (`erun orchestrator set-model <id> --model default`)``; exit code 1; nothing is written. |
| The recorded model is the literal `default` (any case). | Aborts with ``orchestrator <id>: invalid model "default": it is the token for clearing the model, not a model id -- name another model or clear it (`erun orchestrator set-model <id> --model default`)``; exit code 1; nothing is written. `set-model` itself never records it, because `--model default` clears the model, so this is reachable only from a hand-edited config or a value written by the desktop; `set-effort` and `set-gateway` refuse the same way, since they validate the whole orchestrator. |
| The orchestrator uses the gateway and the model is one it cannot drive. | Aborts with ``orchestrator <id>: model "<model>" cannot be driven through the gateway: its provider requires its own reasoning echoed back -- choose another model (`erun orchestrator set-model <id> --model <model>`), or keep the gateway off``; exit code 1; nothing is written. |
| The orchestrator's gateway is on but the gateway catalog has since been removed. | Aborts with ``orchestrator <id>: the gateway is turned on but no gateway catalog is configured -- configure the gateway catalog in the desktop's ERun settings, or turn the gateway off (`erun orchestrator set-gateway <id> --gateway off`)``; exit code 1; nothing is written. |
| The orchestrator id doesn't exist. | Aborts with `orchestrator "<id>" not found`; exit code 1. |
| The id is omitted. | Cobra's argument check aborts before the command runs with `accepts 1 arg(s), received 0`; exit code 1. |
| The id is empty or whitespace (`""`). | Aborts with ``set orchestrator model: no orchestrator id given — pass one explicitly (`erun orchestrator set-model <id> --model <model\|default>`)``; exit code 1; nothing is written. |

## `erun orchestrator set-effort`

Set the effort an orchestrator's session launches at: one of `low`, `medium`, `high`, `xhigh`, `max`, or `ultracode`, the same levels an environment's AI tab takes (see [`claude.effort`](/reference/configuration#envconfig)). Pass `default` to clear it back to `ultracode`. The five `--effort` levels launch as `claude --effort <level>`; `ultracode` launches as `claude --settings '{"ultracode":true}'`.

### Synopsis

```
erun orchestrator set-effort ORCHESTRATOR_ID --effort <level|default> [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--effort` | Required. `low`, `medium`, `high`, `xhigh`, `max`, `ultracode`, or `default` to clear it back to `ultracode`. |
| `--dry-run` | Validate and trace the write (`write orchestrator <id> effort`) without making it. |

### Examples

```bash
erun orchestrator set-effort my-orchestrator --effort high
erun orchestrator set-effort my-orchestrator --effort default
```

### Error behaviour

| Failure | Behaviour |
|---|---|
| `--effort` missing. | Cobra-required; aborts before the command runs. |
| `--effort` is empty. | Aborts with `invalid effort "": must be one of low, medium, high, xhigh, max, ultracode, or "default" to clear it`; exit code 1; nothing is written. |
| `--effort` is not one of the levels. | Aborts with ``orchestrator <id>: invalid effort "<value>": must be one of low, medium, high, xhigh, max, ultracode -- name a known level or clear it (`erun orchestrator set-effort <id> --effort default`)``; exit code 1; nothing is written. |
| The orchestrator id doesn't exist. | Aborts with `orchestrator "<id>" not found`; exit code 1. |
| The id is omitted. | Cobra's argument check aborts before the command runs with `accepts 1 arg(s), received 0`; exit code 1. |
| The id is empty or whitespace (`""`). | Aborts with ``set orchestrator effort: no orchestrator id given — pass one explicitly (`erun orchestrator set-effort <id> --effort <level\|default>`)``; exit code 1; nothing is written. |

Any of the three commands also refuses, with the same `orchestrator <id>: <reason> -- <remedy>` shape, when a value already recorded on the orchestrator fails validation — which only a hand-edited `config.yaml` can produce. Fix that value with its own command (or pass `default`), then retry.
