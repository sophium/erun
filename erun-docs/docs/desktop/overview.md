---
title: Desktop app overview
---

# Desktop app

The ERun desktop app wraps the same CLI in a native UI optimized for managing many tenants and environments side by side.

## Key features

- **Sidebar of tenants and environments.** Quickly switch between projects without retyping commands.
- **Per-environment terminal sessions.** Each environment owns persistent PTY-backed sessions; switching tabs doesn't kill your shell.
- **Edit modal.** General settings, runtime image and resources, AI tooling, port mapping, SSH key, and cloud context — all in one place.
- **Cloud context status in real time.** Start, stop, and watch managed clusters from the sidebar.
- **MCP port-forwards.** The app keeps a local port-forward to each open environment's `erun-mcp` container, so AI tools can talk to it directly over JSON-RPC.

## Install

Download the latest installer from the [GitHub releases](https://github.com/sophium/erun/releases/latest) page (macOS `.dmg`, Windows `.exe`).
