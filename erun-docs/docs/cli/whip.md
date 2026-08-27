---
title: erun whip
---

# `erun whip`

Push every live orchestrator and environment agent to keep moving, on demand, and report exactly who was pushed and who was skipped — and why.

## Why this exists

The pacing nudge already runs automatically: the desktop re-states "keep going, don't stop" into an orchestrator's session once it has gone quiet for a while (see [Periodic pacing re-statement](/agent-reference/skills-spec#periodic-pacing-re-statement-and-crash-auto-resume)). But that pass only ever runs on its own schedule, only ever reaches orchestrators, and reports nothing — an operator who suspects it isn't working has no way to check. `erun whip` is the manual, visible counterpart: one command that pushes the same nudge into everything it can reach right now, and prints what happened to each target.

## Synopsis

```
erun whip [TENANT ENVIRONMENT] [flags]
```

## What "live" means, and what disqualifies a target

Two populations, each disqualified for its own explicit reason rather than lumped into one generic failure:

- **Environment agents.** Each configured environment has its own AI session (the one `erun open`'s AI tab reattaches to). `whip` calls that environment's own `whip` MCP tool. An environment nobody currently has open in the desktop has no reachable edge and is reported **skipped — not alive**: there is no live session there to push. One that already answered the maximum number of consecutive nudges without showing new activity is reported **skipped — already-capped**, and pushing it again does not restart the count — see [Reply or restart](#reply-or-restart) below.
- **Orchestrators.** A persisted orchestrator's live session is a PTY held entirely inside the running desktop process — a separate `erun` invocation (this command) has no channel into it. Every configured orchestrator is therefore reported **skipped — unreachable from this transport**, always, regardless of whether it is actually running. Only the desktop itself — its own automatic pass, or the titlebar's whip button (see [Desktop app overview](/desktop/overview#orchestrators)) — can push an orchestrator; this is a structural limit of the CLI, not a bug.

Neither case is a hard command failure — `erun whip` exits 0 and reports every target's outcome. That report *is* the deliverable: naming who was pushed and who was skipped, and why, is what makes "it isn't working" a checkable claim instead of a feeling.

## Explicit means explicit

Passing `TENANT ENVIRONMENT` (or omitting both to whip everything configured) pushes each live target immediately — it does not wait for that session to have gone quiet for a while first. Running whip is the operator asserting "push this now", the same assertion the desktop's own titlebar whip button makes when clicked. It never bypasses the per-target cap: a session already at its limit is still reported capped, not pushed a seventh time.

## Reply or restart {#reply-or-restart}

A capped target has stopped answering after repeated nudges — erun has already re-stated the pacing contract several times with no sign the session resumed. Whipping it again does not help; either the session needs a reply in its own pane, or it needs restarting. The cap resets automatically once the session shows fresh activity on its own.

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target one environment (both required together). Omit both to whip every configured environment plus every persisted orchestrator. |
| `--dry-run` | Resolve and report what would happen — which targets would be pushed, skipped, or capped — without writing anything into any session. Still calls each reachable environment's edge (with its own preview flag set, so nothing is written) to give a real answer rather than a guess. |
| `--json` | Emit the full report as JSON. |

## Examples

```bash
# Whip everything configured: every environment, every persisted orchestrator.
erun whip

# Whip one environment.
erun whip --tenant my-tenant --environment dev

# See what would happen without pushing anything.
erun whip --dry-run
```

## Configuration

The nudge message, the staleness threshold, and the consecutive-nudge cap are configurable in `~/.erun/config.yaml`, under a `whip` section — editable without a rebuild, and unset by default (an install that configures nothing keeps exactly today's text and bounds):

```yaml
whip:
  message: "Keep going — commit incrementally and run the gate in the foreground."
  staleafterseconds: 600
  maxnudges: 6
```

| Key | Default | Effect |
|---|---|---|
| `message` | The built-in pacing text | What gets typed into a nudged session. |
| `staleafterseconds` | `600` (10 minutes) | How long the *automatic* desktop pass waits before nudging a quiet orchestrator. `erun whip`'s own explicit pass ignores this — see [Explicit means explicit](#explicit-means-explicit). |
| `maxnudges` | `6` | Consecutive un-answered nudges before a target is capped instead of nudged again. |

This section is read by the desktop's automatic pass, by every environment's own `whip` MCP tool, and by this command — one configuration, every surface.

## From an MCP-connected orchestrator

The environment half of this command reaches an Agent through the `whip` MCP tool, scoped to that server's own environment — see [MCP overview · `whip`](/mcp/overview#whip).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Only one of `--tenant`/`--environment` given. | Errors, naming the conflict; nothing is read or pushed. |
| An environment has no reachable MCP edge. | Reported in the output as skipped — not alive; the command itself still exits 0. |
| A persisted orchestrator. | Always reported skipped — unreachable from this transport; the command itself still exits 0. |
| No environments and no orchestrators configured at all. | Exits 0 with an empty report. |
