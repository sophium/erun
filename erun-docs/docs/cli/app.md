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

## Which copy is launched

Two copies of the desktop app can exist at once: one beside the `erun` binary, where an installed or hand-placed copy lands, and one in a checkout's `erun-ui/bin`, where `./erun-ui/build.sh` writes its rebuild. `erun app` launches whichever copy was written most recently, and reports which one that is:

```
app: launching /Users/me/erun/erun-ui/bin/ERun.app (newest of 2 copies found; not launched: /Users/me/erun/erun-cli/bin/ERun.app)
```

For an `.app` bundle, the time that counts is the later of the bundle itself and the executable inside it, so a rebuild counts whether it recreates the bundle or replaces the binary within it. Two copies written at the same moment cannot be told apart, and the one beside the `erun` binary is launched.

If a rebuild appears to have changed nothing, that line names the copy that actually started. A desktop restarts from the copy it is already running from, so a stale copy keeps relaunching itself: quit the app and launch it again with `erun app` to move onto the newest copy, and remove the copy you no longer want so it cannot be picked again.

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
| `--orchestrator` | Id of the orchestrator that owns the terminal pane after the restart. Defaults to `$ERUN_ORCHESTRATOR_ID`. |
| `--dry-run` | Resolve the target and report what a restart would reopen, without restarting anything. |
| `--output` | `text` (default) or `json`. |

### Examples

```bash
erun app restart
erun app restart --orchestrator my-orchestrator
erun app restart --dry-run
```

### What a restart reopens {#erun-app-restart-what-reopens}

Every orchestrator that was open comes back, and the one named by `--orchestrator` owns the terminal pane. Orchestrators run as Claude Code background agents, so the restart does not interrupt them — the one that asked for the restart included — and the new desktop reattaches each one to its agent, on whatever conversation that agent is on (see [Desktop app · The conversation an orchestrator comes back to](/desktop/orchestrators#orchestrator-conversations)).

What can still go wrong is a desktop that would refuse to reopen one: an orchestrator whose gateway has no catalog behind it, say, or whose gateway key is gone. `--dry-run` names those before the restart, while they can still be fixed:

```
would restart the running desktop app (pid 41827)
it would reopen 2 orchestrators, and 1 of them would NOT be reopened:
  The restart will not reopen petios-ops: the gateway is turned on but no gateway catalog is configured. Its background agent keeps running; fix that and open it again.
```

When every orchestrator would reopen, it says so in one line.

The plan is computed by the running desktop — the only process that can resolve it — and asking for it writes nothing, launches nothing, and quits nothing. With `--output json` the same plan arrives as a `preview` array on the result.

The question travels on an address of its own, so a desktop older than this command cannot mistake it for the command. Such a desktop does not serve that address at all: the dry run reports that it is too old to answer rather than restarting it, and the desktop is left running.

### Error behaviour

| Failure | Behaviour |
|---|---|
| No desktop app is running. | Refuses with "no desktop app is currently running"; exit code 1. |
| The desktop process recorded as running has exited (a stale record). | Refuses, naming the stale pid; exit code 1. |
| The desktop app is running but declines the restart. | Reports the failure it returned; exit code 1. |
| The desktop app is running but does not answer the `--dry-run` plan. | Reports that the plan could not be read, alongside the restart it still would have performed; exit code 0. |
| The desktop app is older than the `--dry-run` plan request. | Reports that it does not serve the plan request; the desktop is left running; exit code 0. |
| The desktop has no orchestrator open. | `--dry-run` reports that it has none to reopen; exit code 0. |
