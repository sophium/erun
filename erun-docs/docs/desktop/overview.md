---
title: Desktop app overview
---

# Desktop app

The ERun desktop app is **how most people use ERun day-to-day**. It runs on **macOS** and **Windows** and gives you a single control panel for everything: your projects, your environments, the Agents attached to them, and the cloud machines that back them.

For CI/CD pipelines and headless workflows, use the [CLI](/cli/overview) instead.

## Control panel

- **Sidebar of projects and environments.** Every project and environment at a glance, with live status — running, stopping, idle, errored. Switch between them without typing commands.
- **One-click lifecycle.** Start, stop, restart, and delete environments from the sidebar.
- **Cloud status in real time.** Start, stop, and watch the cloud machines that back remote environments. The sidebar shows status as they wake up or shut down.
- **Settings in one place.** Runtime sizing, AI tooling configuration, port mappings, SSH keys, and cloud bindings — all editable from one screen per environment.

## Open it in your editor

- **Persistent terminal sessions.** Each environment owns its own terminals. Switching tabs doesn't kill them. Your scrollback survives.
- **VS Code or IntelliJ in one click.** The IDE attaches to the environment's filesystem — editor, extensions, language servers, debugger, everything sees the same files the Agent sees.
- **Any other editor that supports SSH.** Cursor, Zed, JetBrains Gateway, Neovim with remote plugins, anything else — the desktop publishes the local SSH details, point your editor at them.

## Works with or without an Agent

The desktop is equally useful as a clean development surface for human-driven work — open as many isolated environments as your machine can host, develop in any one, and let ERun handle the infrastructure underneath.

## Side by side with an Agent

By default, **the Agent runs inside the env** — the runtime pod ships the configured Agent's CLI (`claude`, `codex`, …) pre-wired against the in-pod MCP loopback. The desktop's AI panel surfaces it; any terminal inside the pod can launch it directly.

When you do want a laptop-side Agent in addition, the env has two endpoints on the runtime pod — SSH and [MCP](/mcp/overview) — and both accept any client.

- IDEs (VS Code, IntelliJ, Cursor, Zed, …) attach over SSH.
- **The Claude Code desktop app and Codex desktop app attach the same way** — they open the env as a remote workspace, edit files, run commands. They also use MCP for structured ERun operations (`idle`, `list`, `doctor`, `build`, …).
- Custom agents (any MCP client) typically stick to MCP for structured calls and reach for SSH only when they need shell access.

A commit you make in VS Code is immediately visible to the Agent's next file read. An action the Agent takes shows up in your terminal scrollback and in the [audit trail](/collaboration/operator-in-the-loop). No parallel worlds.

## Workspace sync (optional)

You don't normally need workspace sync — the runtime pod already sees your project repo (via host mount in a local-agent env, or via PVC checkout in a remote-agent env). Edits in your IDE are immediately visible to the Agent.

Workspace sync covers a narrower case: making files **outside** the project repo visible inside the pod — editor scratch files, local notes you want the Agent to read, secrets you want in the pod's home directory without committing.

Enable it from the desktop's env settings panel and pick the local folder to mirror. The desktop polls the folder and pushes new or changed files into the pod; it's **one-way only** (local → pod), and stale files on the pod side are cleaned up automatically after a grace period. For the polling cadence, the cleanup semantics, and the size budget, see [Agent reference · Workspace sync spec](/agent-reference/workspace-sync-spec).

The sync is a convenience for ancillary files. Code lives in the repo; the repo is mounted or checked out separately.

## Install

Install ERun [via Homebrew or Scoop](/getting-started/install). One command installs both the CLI and the desktop app.
