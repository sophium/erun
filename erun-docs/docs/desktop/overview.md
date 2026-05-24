---
title: Desktop app overview
---

# Desktop app

The ERun desktop app is your **control panel** for the platform. It wraps the CLI in a native UI optimized for managing many tenants and environments side by side, and it's the surface most operators use for day-to-day work.

## Control panel

- **Sidebar of tenants and environments.** Every tenant and environment at a glance, with live status (running, stopping, idle, errored). Switch projects without retyping commands.
- **One-click lifecycle.** Start, stop, restart, and delete environments from the sidebar. No `kubectl` invocations, no helm commands.
- **Cloud context status in real time.** Start, stop, and watch the managed Kubernetes clusters that back remote environments. Status updates as the cluster transitions.
- **Edit modal.** General settings, runtime image and resources, AI tooling, port mapping, SSH key, and cloud context — all in one place.

## Develop anywhere

- **Persistent terminal sessions.** Each environment owns PTY-backed shell sessions; switching tabs doesn't kill them. Scrollback survives.
- **Open in your IDE.** Hand any environment over to VS Code (Remote-SSH) or IntelliJ IDEA (Gateway) with one click. The IDE runs against the in-pod SSH server, so the editor, extensions, language servers, and debugger all see the environment's filesystem. The same `erun open --vscode` / `--intellij` flags are available from the CLI.
- **Any Remote-SSH-capable IDE.** Cursor, Zed, JetBrains products in general, Neovim with remote plugins — anything that can attach over SSH connects to the same endpoint. The desktop publishes the local SSH port and SSH config block; you point your editor at it.

## AI tooling parity

- **MCP per environment.** The app keeps a local port-forward to each open environment's `erun-mcp` container, so Claude, Codex, and any MCP-compatible tool can talk to it directly over JSON-RPC. Per-env ports are published as JSON files under `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json` for discovery.
- **One environment, two interfaces.** When you're in VS Code and Claude is in MCP, you're both seeing the same runtime pod. A commit one of you makes is immediately visible to the other. There is no parallel-universe problem.

## Works with or without agents

The desktop is not agent-specific. It is equally useful as a clean dev surface for human-driven work: open as many isolated environments as your machine can host, develop in whichever you want via shell or IDE, and let ERun handle the Kubernetes, Docker, build cache, and idle-stop concerns underneath.

## Works with or without agents

The desktop app is not agent-specific. It is equally useful as a clean dev surface for human-driven work: open as many isolated environments as your machine can host, develop in whichever you want via shell or IDE, and let ERun handle the Kubernetes, Docker, build cache, and idle-stop concerns underneath.

## Install

Download the latest installer from the [GitHub releases](https://github.com/sophium/erun/releases/latest) page (macOS `.dmg`, Windows `.exe`).
