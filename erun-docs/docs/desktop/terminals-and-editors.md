---
title: Terminals and editors
---

# Terminals and editors

Opening an environment in the desktop is opening it in your editor of choice, backed by terminals that keep running even while you're looking elsewhere.

### Persistent terminal sessions

Each environment owns its own terminals, and switching tabs doesn't kill them. For an environment backed by a runtime pod, the sessions live in the pod and keep running even while the environment is closed — a long-running Agent keeps working while you're away — so reopening reconnects you to the same sessions and scrollback instead of starting fresh ones. Switching between tabs is instant regardless of how long a session has been running or how much it has printed: the desktop keeps a snapshot of each tab's screen and only replays what's arrived since you last looked at it, instead of re-drawing its entire history. A single tab's retained scrollback is capped (the same size xterm itself keeps on screen) so a session that runs for days, or one nobody has looked at in a while, can't grow without bound in memory.

### Clickable URLs and file paths

A URL an Agent or command prints — `https://`, `mailto:`, or one wrapped in a terminal hyperlink — opens in your browser on click. A file path opens with your OS's file handler the same way. Where the path points depends on which kind of tab printed it: the **Local** tab, an orchestrator, and the Contribute shell all run on your own machine, so a path there is a real path on your machine. An environment's own tabs (**ERun**, **AI**, and any extra terminal) run inside that environment's pod, so a path there is only opened if it has a real counterpart on your machine — under [workspace sync's mirror](/desktop/workspace-sync) or its synced outputs folder. A pod path with no synced counterpart is left as plain text rather than guessing; it is never opened against a same-named file that happens to exist on your own machine.

### Copying out of a terminal

Select text in a terminal tab and copy it with your platform's chord — **Cmd+C** on macOS, **Ctrl+C** or **Ctrl+Shift+C** on Windows — and it lands on your machine's clipboard, not the pod's. Ctrl+C stays the interrupt: with nothing selected it always reaches the program in the terminal, and on macOS it does so even when something is selected. Right-clicking copies the selection too, or pastes when there is none. A program running inside the environment can also hand you text directly — an Agent that prints a sign-in URL with a "press c to copy" hint puts it on **your** clipboard, so you can paste it into the browser on your machine. That direction is one-way by design: nothing inside the environment can read your clipboard back.

- **VS Code or IntelliJ in one click.** The IDE attaches to the environment's filesystem — editor, extensions, language servers, debugger, everything sees the same files the Agent sees.
- **Any other editor that supports SSH.** Cursor, Zed, JetBrains Gateway, Neovim with remote plugins, anything else — the desktop publishes the local SSH details, point your editor at them.

## Where next

- [Working with an Agent](/desktop/working-with-an-agent) — the same SSH endpoint, from an Agent's perspective.
- [Workspace sync](/desktop/workspace-sync) — mirroring a remote-agent env's worktree to a local folder.
