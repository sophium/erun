---
title: Desktop app overview
---

# Desktop app

The ERun desktop app wraps the same CLI in a native UI optimized for managing many tenants and environments side by side.

## Key features

- **Sidebar of tenants and environments.** Quickly switch between projects without retyping commands.
- **Per-environment terminal sessions.** Each environment owns persistent PTY-backed sessions; switching tabs doesn't kill your shell.
- **Open in your IDE.** Hand any environment over to VS Code (Remote-SSH) or IntelliJ IDEA (Gateway) with one click. The IDE runs against the in-pod SSH server, so the editor, extensions, language servers, and debugger all see the environment's filesystem. The same `erun open --vscode` / `--intellij` flags are available from the CLI.
- **Edit modal.** General settings, runtime image and resources, AI tooling, port mapping, SSH key, and cloud context — all in one place.
- **Cloud context status in real time.** Start, stop, and watch managed clusters from the sidebar.
- **MCP port-forwards.** The app keeps a local port-forward to each open environment's `erun-mcp` container, so AI tools can talk to it directly over JSON-RPC.

## Works with or without agents

The desktop app is not agent-specific. It is equally useful as a clean dev surface for human-driven work: open as many isolated environments as your machine can host, develop in whichever you want via shell or IDE, and let ERun handle the Kubernetes, Docker, build cache, and idle-stop concerns underneath.

## Install

Download the latest installer from the [GitHub releases](https://github.com/sophium/erun/releases/latest) page (macOS `.dmg`, Windows `.exe`).
