---
title: Control panel
---

# Control panel

The sidebar and its environment rows are where most sessions with the desktop app start and end — a live status for every project and environment, without typing a command.

### Sidebar of projects and environments

Every project and environment at a glance, with live status — running, stopping, idle, errored. Switch between them without typing commands. An open environment carries a status indicator that reflects its real condition, not just that its tabs exist: a green dot while it runs, a hollow ring when its cloud machine is stopped (start it from the titlebar), and a warning triangle when its deploy failed or reconnecting gave up (recover from the [Activities panel](/desktop/activities-and-recovery)). The triangle also covers an environment that has stopped answering: the desktop re-establishes a dead connection on its own — whether the connection is wedged or gone outright, which is what an ordinary pod replacement leaves behind — and when repeated attempts do not help it flags the row as unreachable, rather than letting it read as quiet or as an environment nobody opened. See [Agent can't reach MCP](/reference/troubleshooting#agent-cant-reach-mcp). Clicking the indicator closes the environment's tabs.

A **second, separate indicator** on the same row reports the cloud machine the environment's cluster runs on. That is a different fact about a different thing, with a different remedy, so it gets its own symbol rather than reusing the environment's: a crossed-out power symbol when the machine is stopped (select the environment and start it from the titlebar), a server outline while it is starting, and a question mark when its state could not be checked. A question mark is never shown as "stopped" — "we could not tell" and "it is off" send you to different places, so they never share a symbol. A machine that is running adds no symbol to the row; the hover card names it and its state either way. An environment with no cloud machine ERun manages — one on a local cluster, or a host environment — shows nothing here, and the hover card says so plainly rather than leaving you to guess.

### The sidebar collapses on its own on a narrow window

Shrink the window enough and the sidebar folds away so the main panel — a review, the tenant dashboard, a terminal — always keeps enough room to stay usable rather than being squeezed into clipped tabs and unreachable buttons. Toggle it open or closed yourself from the titlebar and that choice sticks: the sidebar stays as you left it through further resizes, until you toggle it again. A collapse the app made on its own, on the other hand, reverses itself automatically once you widen the window back out. The toggle button in the titlebar is always there, so a collapsed sidebar is never more than one click from back.

- **Dark and light theme.** The sun/moon button in the titlebar switches between light and dark. ERun starts in whichever your OS prefers, and once you pick a theme yourself that choice sticks across relaunches even if your OS preference later changes.
- **Environment details on hover.** Hover an environment row to see its runtime version, the issue it's working on (the current git branch and, when the branch names an issue, its title), what it's doing right now — the operation in flight, **Stopped**, **Deploy failed**, **Not open**, or **Idle** — its usage, and the cloud machine behind it with the state that machine was last observed in. Branch and issue come from your machine's worktree for local environments and from inside the pod for remote environments while they're open; an environment that isn't open says so instead of guessing.
- **Two version lines, told apart.** A project can ship its own build line (its own tags, its own numbering) that still runs an ERun release underneath — so the runtime version on the card is labelled with which line it belongs to, ERun's own or the project's own, rather than shown as a bare number that would otherwise read as an ERun version even when it isn't. Where the project's own line applies, the card adds a separate **Erun version** row for the ERun release actually running underneath it; when the two coincide, the card says so instead of showing the same number twice. If a redeploy would pull a different line than what's currently running, the card flags that disagreement too.
- **Usage on the hover card, compared at a glance.** The same card shows a compact CPU/memory reading — "CPU 12.0% · Mem 25% of 2048Mi" — so you can compare environments without opening Manage for each one. The figure is read periodically in the background, not on hover, and is labelled with how long ago it was taken; a reading older than that refresh interval is marked **Stale** rather than shown as if it were current. An environment with no pod to measure — stopped, or a host environment — says so plainly instead of showing a 0%, which would otherwise read as idle and healthy rather than unmeasured. For the environment's own resource readings, see [Resources and usage](/desktop/resources-and-usage).
- **One-click lifecycle.** Start, stop, restart, and delete environments from the sidebar.
- **Cloud status in real time.** Start, stop, and watch the cloud machines that back remote environments. The sidebar shows status as they wake up or shut down.

## Where next

- [Deploying a version](/desktop/deploying-a-version) — the Runtime tab's version and chart pickers.
- [Resources and usage](/desktop/resources-and-usage) — an environment's own CPU, memory, and disk readings.
- [Settings and ports](/desktop/settings-and-ports) — runtime sizing, AI tooling, and public address management.
