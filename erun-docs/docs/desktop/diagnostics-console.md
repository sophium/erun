---
title: Diagnostics console
---

# Diagnostics console

The panel at the bottom of the terminal area follows whatever you're looking at, so it always has real evidence rather than a blank pane:

- **An environment selected.** The env's persistent trace log: every erun command that ran for it, at full trace detail, timestamped — including commands that finished before you opened the console. Capture is always on; the log is capped and rotated automatically (see [where it lives](/reference/config-locations#trace-log)). For a remote environment the timeline merges two vantage points: the commands you ran from this machine and, while the environment is open, the Agent-driven ones from inside the pod — the in-pod lines are marked `[pod]`.
- **An orchestrator session focused.** Its name, status, background shell (if any), and the environments it links — plus the desktop's own log, since an orchestrator fault is desktop-side, not env-side.
- **Neither.** The desktop's own log on its own, for a fault that belongs to the app itself.

**UI trace** — the desktop's own action history, what the app just did, in order — sits in a second tab next to whichever of the above is active, for reporting a desktop bug rather than an environment or orchestrator one.

Both panes have a **Copy** button for their own stream, and the console's **Copy report** button packages everything at once — app build, the active context's identity and state, and the UI trace — into a single paste-ready block, so a bug report carries the evidence instead of a description of it. **Report an erun issue** goes one step further: it opens a prefilled `github.com/sophium/erun` issue in your browser (title and reproduction/environment sections filled in from the same evidence) for you to review, fill in what happened, and submit yourself — it never files anything on your behalf. The full report stays on your clipboard too, so nothing is lost even if the prefilled body is trimmed to fit the link.

On a busy environment the erun trace can be a wall of scrollback. **Clear** baselines the view — it hides the lines shown so far so whatever happens next stands out — without deleting anything: the persistent log stays intact, **Show all** brings the earlier lines back, and Copy and Copy report always include the full log. The baseline is per-environment, so switching environments starts fresh.

## Where next

- [Activities and recovery](/desktop/activities-and-recovery) — the failed-deploy entry the console's evidence backs up.
- [Reference · Configuration locations](/reference/config-locations#trace-log) — where the underlying trace log lives on disk.
