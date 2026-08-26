---
title: erun app
---

# `erun app`

Launch the ERun [desktop app](/desktop/overview). Starts the app detached and returns immediately, so you can launch it from a terminal without tying up the shell.

## Synopsis

```
erun app [flags]
```

## Flags

| Flag | Description |
|---|---|
| `--headless` | Run the desktop backend without a window and serve the frontend over HTTP. |
| `--port` | HTTP listen port for `--headless` (defaults to the desktop binary's default). |

Without `--headless`, no flags are forwarded — the app opens its normal window.

## Examples

```bash
erun app
erun app --headless --port 34115
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| `erun-app` binary / bundle not found. | Errors with "build or install it first". |

## `erun app restart` {#erun-app-restart}

Restart an already-running desktop app in place — the same rebuild-and-restart the desktop's own Restart button triggers, from a terminal or a script. Use it after rebuilding erun's own tooling, so the running app picks up the new binary without you having to quit and relaunch it by hand.

It never spawns a second instance or kills a process directly: it asks the one desktop app that is already running to restart itself, so it can't leave two copies open or a process half-killed. Before doing anything it resolves which desktop process is running and confirms that process is still alive — if nothing is running, or the record of what was running is stale, it refuses outright rather than guessing.

### Synopsis

```
erun app restart [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--orchestrator` | Id of the orchestrator to resume after the restart. Defaults to `$ERUN_ORCHESTRATOR_ID`. |
| `--dry-run` | Resolve and report the target without restarting anything. |
| `--output` | `text` (default) or `json`. |

### Examples

```bash
erun app restart
erun app restart --orchestrator my-orchestrator
erun app restart --dry-run
```

### Error behaviour

| Failure | Behaviour |
|---|---|
| No desktop app is running. | Refuses with "no desktop app is currently running"; exit code 1. |
| The desktop process recorded as running has exited (a stale record). | Refuses, naming the stale pid; exit code 1. |
| The desktop app is running but declines the restart. | Reports the failure it returned; exit code 1. |
