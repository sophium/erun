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

## The titlebar's message centre {#message-centre}

The titlebar keeps its own running record of what it's told you, separate from the console above. Instead of one banner that the next message replaces, it shows one icon per class — error, warning, info, success — each carrying a count of what you haven't read yet. An icon only appears once there's something in its class to see; clicking it opens a dialog listing every message of that class, newest first, in full — nothing is ever truncated the way a banner would have to truncate it. A success or info message clears its own count a few seconds after it appears, the way a toast would, but it isn't deleted: it stays in the dialog for the rest of the session, so a confirmation you glanced past is still there if you go looking. That countdown only runs while the window is focused — switch away before it finishes and it picks up again, with whatever time was left, once you switch back, so a message that appeared while you were in another application is never marked read before you could have seen it. A warning or error stays counted as unread until you open it.

Every message in the dialog carries the same reporting action the console's own button does, ahead of the browser form: a known remedy (Deploy, Restart) leads, and **Report a bug** always follows it on an error — or leads on its own when there's no remedy to name. Rather than a form you fill in, it hands the failure to an Agent: the Agent searches for a matching open issue first and proposes commenting on it instead of filing a duplicate, then drafts the report from the full captured output (not trimmed to fit a link) in its own session for you to review before anything is submitted. If no Agent is available to draft it, the message falls back to the same prefilled-browser-form path as the console's own button, and says why. Every message also has its own **Copy**, so anything you're reading here can go straight into a bug report or a chat without retyping it.

Some messages are tagged with the environment or orchestrator they describe, and the dialog shows that alongside the message. A handful of diagnostic messages that don't matter for day-to-day use are filed under a separate debug class, hidden by default — reveal them with the toggle at the top of the dialog if you're digging into something deeper.

On a busy environment the erun trace can be a wall of scrollback. **Clear** baselines the view — it hides the lines shown so far so whatever happens next stands out — without deleting anything: the persistent log stays intact, **Show all** brings the earlier lines back, and Copy and Copy report always include the full log. The baseline is per-environment, so switching environments starts fresh.

## Where next

- [Activities and recovery](/desktop/activities-and-recovery) — the failed-deploy entry the console's evidence backs up.
- [Reference · Configuration locations](/reference/config-locations#trace-log) — where the underlying trace log lives on disk.
