---
title: Desktop app overview
---

# Desktop app

The ERun desktop app is **how most people use ERun day-to-day**. It runs on **macOS** and **Windows** and gives you a single control panel for everything: your projects, your environments, the Agents attached to them, and the cloud machines that back them.

For CI/CD pipelines and headless workflows, use the [CLI](/cli/overview) instead.

## Control panel

- **Sidebar of projects and environments.** Every project and environment at a glance, with live status — running, stopping, idle, errored. Switch between them without typing commands. An open environment carries a status indicator that reflects its real condition, not just that its tabs exist: a green dot while it runs, a hollow ring when its cloud machine is stopped (start it from the titlebar), and a warning triangle when its deploy failed or reconnecting gave up (recover from the Activities panel). Clicking the indicator closes the environment's tabs.
- **Environment details on hover.** Hover an environment row to see its runtime version, the issue it's working on (the current git branch and, when the branch names an issue, its title), and what it's doing right now — the operation in flight, **Stopped**, **Deploy failed**, **Not open**, or **Idle**. Branch and issue come from your machine's worktree for local environments and from inside the pod for remote environments while they're open; an environment that isn't open says so instead of guessing.
- **One-click lifecycle.** Start, stop, restart, and delete environments from the sidebar.
- **Cloud status in real time.** Start, stop, and watch the cloud machines that back remote environments. The sidebar shows status as they wake up or shut down.
- **Upgrade all.** The **Upgrade all** button in the Environments header redeploys every environment opted into the upgrade set to the latest version for its channel (stable or snapshot). It opens a preview dialog listing each member and its current → target version, and only upgrades the ones that lag — opt an environment in and pick its channel from the environment's Runtime settings. See [`erun upgrade`](/cli/upgrade).
- **Activities panel with failure details and fixes.** A queue of recent and in-flight operations — deploys, opens, builds. When a deploy fails, the entry keeps the captured command output (the real helm/kubectl error behind the one-line summary): expand **Show output** to read it, or use **Copy failure report** to package that output together with the environment, version, and container status so you can hand the whole picture to whoever can help. The failed entry also offers one-click recovery: **Run doctor** to troubleshoot (see [`erun doctor`](/cli/doctor)), **Rebuild & redeploy** to force a clean rebuild, and **Clear pending helm release** when a release is stuck.
- **Settings in one place.** Runtime sizing, AI tooling configuration, port mappings, SSH keys, and cloud bindings — all editable from one screen per environment.

## Open it in your editor

- **Persistent terminal sessions.** Each environment owns its own terminals, and switching tabs doesn't kill them. For an environment backed by a runtime pod, the sessions live in the pod and keep running even while the environment is closed — a long-running Agent keeps working while you're away — so reopening reconnects you to the same sessions and scrollback instead of starting fresh ones.
- **VS Code or IntelliJ in one click.** The IDE attaches to the environment's filesystem — editor, extensions, language servers, debugger, everything sees the same files the Agent sees.
- **Any other editor that supports SSH.** Cursor, Zed, JetBrains Gateway, Neovim with remote plugins, anything else — the desktop publishes the local SSH details, point your editor at them.

## Works with or without an Agent

The desktop is equally useful as a clean development surface for human-driven work — open as many isolated environments as your machine can host, develop in any one, and let ERun handle the infrastructure underneath.

## Side by side with an Agent

By default, **the Agent runs inside the env** — the runtime pod ships the configured Agent's CLI (`claude`, `codex`, …) pre-wired against the in-pod MCP loopback. The desktop's AI panel surfaces it; any terminal inside the pod can launch it directly.

For a Claude env, the AI tab of the env settings dialog carries the Claude launch controls. **Effort** sets how hard Claude works per turn: the levels from low through max trade response time for thinking depth, and **ultracode** — the default for new envs — runs at xhigh thinking effort and additionally turns on standing multi-agent workflow orchestration, so Claude can fan work out across coordinated agents without being asked. Pick a plain level (for example max) when you want pure single-agent thinking instead. **Default model** picks the model the env's Claude session starts on, chosen from the environment's available models — tick `fable` under Available models to make it selectable. **Launch Claude in verbose + debug mode** streams Claude's own diagnostics into the AI tab. The desktop applies your choices when it opens the env's Claude session, and saving a changed launch setting reopens the env's open AI tabs so it takes effect immediately — the Claude conversation resumes where it left off. For the exact levels and how the values resolve, see [Agent reference · Configuration](/reference/configuration).

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

## Contribute to ERun from any environment

The titlebar's **Contribute** toggle (the git-fork icon) lets you patch ERun itself without leaving the env you're working in. It is available for any non-`erun` agent env (local-agent or remote-agent); it is hidden for runtime envs and for the special `erun` tenant.

When you toggle Contribute on, the desktop:

1. Clones the ERun source into `~/git/erun` inside the env (the host for a local-agent env, the runtime pod for a remote-agent env). The clone is idempotent — a checkout that already points at the canonical ERun remote is reused as-is.
2. Installs a small shim at `~/.erun/contribute/bin/erun` that forwards every invocation to the clone's `erun-cli/run.sh`. Subsequent `erun` calls inside the contribute tabs — whether typed at the prompt or spawned as child processes by an Agent — go through that script, which rebuilds the binary from the current source on each run.
3. Opens two extra tabs alongside the env's normal ERun + AI + Local tabs: **ERun (contribute)** is a shell pointing at the clone; **AI (contribute)** is the Agent attached to the clone. Both tabs are marked with the git-fork icon in the tab strip so you never confuse a contribute terminal with the env's own.
4. Adds an **Env / ERun** segmented control in the review panel's changed-files sidebar. Flipping it switches what the diff view shows: the env's worktree or the ERun clone's worktree, side by side as you iterate.

To try your changes to the desktop app itself, click the **Open contribute app** button (external-link icon) that appears next to the Contribute toggle while contribute mode is on. The desktop boots `erun app --headless` inside the contribute terminal — building the binary on the fly from your clone — brings up a port-forward from the env's contribute-app port out to your host, and opens the locally-built desktop app in a new browser tab. Subsequent code edits are picked up on the next rebuild (Ctrl-C the running process in the ERun (contribute) tab and click the launcher again).

Toggle the switch off when you're done. The contribute tabs close, the headless app and its port-forward shut down, the diff view reverts to the env, and the clone stays on disk so re-enabling later is instant.

## Install

Install ERun [via Homebrew or Scoop](/getting-started/install). One command installs both the CLI and the desktop app.
