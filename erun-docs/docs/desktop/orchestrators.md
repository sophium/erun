---
title: Orchestrators
---

# Orchestrators

Above your environments, the sidebar's **ERUN** section lists your host-side orchestrators — AI sessions that coordinate work across the environments you link to them. For an agent or host environment that means reviewing its code on your machine and delegating every change to the Agent inside it; for a runtime environment, linked with the **Runtime** role below, it means operating that environment directly instead. Open an orchestrator's **…** menu to manage it: restart, delete, or reveal the guidance it operates under.

**Orchestrator details on hover.** Hover a running orchestrator's row to see what it's doing and, for each linked environment, its own state rather than just its name: busy (naming the holder when it's known), idle, in outage, or not open from this desktop, plus its usage figure and age when one has been read — the same activity and usage this environment's own hover card reports, so the two can never disagree. A **Nudges** line reports whether ERun has had to restate its pacing contract to a quiet session, how many times, and whether it has stopped nudging after repeated attempts — a capped session needs your reply or a restart, since ERun has stopped acting on its behalf.

**Whip: push everything by hand.** The titlebar carries a whip button (beside the theme toggle) that runs the same pacing pass immediately, across every live orchestrator *and* every environment's own AI session — not only the ones that have gone quiet long enough for the automatic pass to notice on its own. Clicking it opens a report naming every target: **Pushed**, **Capped** (already at the automatic pass's consecutive-nudge limit — reply in its pane or restart it), **Failed** (a push was attempted and refused — the report names the error), or **Skipped**, with the reason (not alive — no live session to push, for example). Whip never restarts or interrupts anything, so it needs no confirmation and is safe to click at any time. See [`erun whip`](/cli/whip) for the same pass from the CLI, and [pacing](/agent-reference/skills-spec#periodic-pacing-re-statement-and-crash-auto-resume) for the message text and cap semantics both share.

**Editing a running orchestrator's environments takes a restart to apply.** Its session already holds the AI tooling for the environments it started with, and that can't be swapped out from under it — so saving a different set in **Manage** doesn't touch a session that's already running. The row's status dot and the hover card both say so ("Restart to apply") until you do, and the manage dialog carries the same **Restart** action right there, next to the reason. Editing a stopped orchestrator needs no restart: its next start already picks up the new set.

**Role, per linked environment.** Beside each checked environment's directory field, a **Role** control says what this orchestrator uses that environment for: **Code** (writes code, iterates fast, not sized for a full regression run), **Build** (checks out pushed branches, runs the gates, cuts releases), **Runtime** (operates the environment directly — deploy, pin, observe), or **Not declared** — the default, and never silently coerced to any of the other three. The control shows only for a checked environment, and a line beside it states what the selected value means. Role is independent of the environment's own type: an agent or host environment accepts any of the four values, while a runtime environment accepts only the Runtime role — the dialog offers just that one choice for it, since a runtime environment has no worktree to review and no in-pod agent to delegate to, and so carries no directory field at all. Changing a role while the orchestrator is running carries the same restart-to-apply notice as changing the linked environment set, so the dialog never implies an edit took effect before you've confirmed it did. `erun orchestrator set-role` (see [CLI · `erun orchestrator`](/cli/orchestrator)) sets the same field from the terminal, so `config.yaml` is never the only writer.

Each orchestrator runs under two layers of guidance, and the dialog opens either one in your editor (VS Code or IntelliJ):

- **Role** — what this orchestrator does. Yours to edit; ERun creates it once and never overwrites it.
- **Shared contract** — the rules every orchestrator follows. ERun-managed and rewritten on every launch, so edits here don't stick.

For the exact files behind these two layers and how they're injected into a session, see [Agent reference · Skills spec](/agent-reference/skills-spec#host-orchestrator-desktop).

## The conversation an orchestrator comes back to {#orchestrator-conversations}

An orchestrator keeps one long conversation, and starting it — after a quit, a reboot, a crash, or a rebuild-and-restart — resumes that conversation rather than beginning a new one. ERun follows the conversation the session itself reports being in, so a session that ends up in a conversation of its own is still the one you get back.

When it can't confirm which conversation that is, it says so instead of coming back looking healthy with hours of the work missing. The message names both conversations — the one it resumed and the one it couldn't vouch for — and the manage dialog is where you settle it.

The dialog's **Conversation** section lists every conversation this orchestrator could resume, newest first, each with when it was last written, how large it is, the folder it was started in, and how it opens, so you can recognise the one holding your work. Each row says what it is to this orchestrator:

- **Live** — its own session reported being in this one.
- **Stranded** — recorded as live by a session ERun can no longer confirm. This is the row to look at when work has gone missing.
- **Attached** — the one you chose.
- **Default** — the conversation this orchestrator's name resolves to, used until anything else is known.
- **Unclaimed** — a conversation on this machine that no orchestrator is using.

**Attach** restarts the orchestrator in the conversation you picked and remembers the choice, so later starts honour it rather than going back to the default; **Use the default** clears it again. Conversations belonging to your other orchestrators are never offered here — the list says how many it left out — because handing one orchestrator another's history is worse than starting fresh.

## Where next

- [Diagnostics console](/desktop/diagnostics-console) — an orchestrator's own log, for a fault that belongs to the app itself.
- [`erun whip`](/cli/whip) — why the CLI can only report orchestrators as unreachable, and the desktop is the only place that can push them.
