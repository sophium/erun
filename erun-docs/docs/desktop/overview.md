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
- **Deploy a version from the Runtime tab.** An environment's settings → **Runtime** carries a **Version to deploy** picker. Choose a published version and **Deploy** installs exactly that version by reference — it never rebuilds — so the button stays disabled until you pick one. The same picker lists the component charts to roll out, and you can save them as this environment's default; for a runtime environment (which deploys published charts by reference) that list is scoped to the version you pick, so you choose a version first. When the environment builds from your local source, a separate **Create & deploy new version** action builds a fresh version, publishes it, and deploys it in one step. See [command primitives](/concepts/command-primitives).
- **Upgrade all.** The **Upgrade all** button in the Environments header redeploys every environment opted into the upgrade set to the latest version for its channel (stable or snapshot; a snapshot-channel environment adopts a stable release once one is published on top of the latest snapshot). It opens a preview dialog listing each member and its current → target version, and only upgrades the ones that lag — opt an environment in and pick its channel from the environment's Runtime settings. Confirming runs each member's upgrade **in its own environment, in parallel**: progress and any failure show up on that environment's Local tab, sidebar row, and Activities entry, not in someone else's terminal. A member whose latest version can't be determined (for example the registry refused the lookup) shows **latest unknown** with the reason and is left untouched rather than guessed at. When an environment's [listed registries](/deployment/registries) offer more than one newer version, the row shows a **picker** — choose the version (each labelled with the registry it came from) and that member joins the upgrade. See [`erun upgrade`](/cli/upgrade).
- **Activities panel with failure details and fixes.** A queue of recent and in-flight operations — deploys, opens, builds. When a deploy fails, the entry keeps the captured command output (the real helm/kubectl error behind the one-line summary): expand **Show output** to read it, or use **Copy failure report** to package that output together with the environment, version, and container status so you can hand the whole picture to whoever can help. The failed entry also offers one-click recovery: **Run doctor** to troubleshoot (see [`erun doctor`](/cli/doctor)), **Rebuild & redeploy** to force a clean rebuild, and **Clear pending helm release** when a release is stuck.
- **Settings in one place.** Runtime sizing, AI tooling configuration, port mappings, SSH keys, and cloud bindings — all editable from one screen per environment.

## Diagnostics console {#diagnostics-console}

When something misbehaves, the panel at the bottom of the terminal area gives you two paste-ready diagnostic surfaces — built for handing to whoever (or whatever Agent) is helping you debug:

- **erun trace.** The selected environment's persistent trace log: every erun command that ran for the env, at full trace detail, timestamped — including commands that finished before you opened the console. Capture is always on; the log is capped and rotated automatically (see [where it lives](/reference/config-locations#trace-log)). For a remote environment the timeline merges two vantage points: the commands you ran from this machine and, while the environment is open, the Agent-driven ones from inside the pod — the in-pod lines are marked `[pod]`. When the pod can't be reached, the console says so and still shows your machine's side.
- **UI trace.** The desktop's own action history — what the app just did, in order — for reporting a desktop bug rather than an environment one.

Both panes have a **Copy** button for their own stream, and the console's **Copy report** button packages everything at once — app build, the selected environment's identity and state, the erun trace (or the reason there isn't one), and the UI trace — into a single paste-ready block, so a bug report carries the evidence instead of a description of it.

On a busy environment the erun trace can be a wall of scrollback. **Clear** baselines the view — it hides the lines shown so far so whatever happens next stands out — without deleting anything: the persistent log stays intact, **Show all** brings the earlier lines back, and Copy and Copy report always include the full log. The baseline is per-environment, so switching environments starts fresh.

## Open it in your editor

- **Persistent terminal sessions.** Each environment owns its own terminals, and switching tabs doesn't kill them. For an environment backed by a runtime pod, the sessions live in the pod and keep running even while the environment is closed — a long-running Agent keeps working while you're away — so reopening reconnects you to the same sessions and scrollback instead of starting fresh ones.
- **VS Code or IntelliJ in one click.** The IDE attaches to the environment's filesystem — editor, extensions, language servers, debugger, everything sees the same files the Agent sees.
- **Any other editor that supports SSH.** Cursor, Zed, JetBrains Gateway, Neovim with remote plugins, anything else — the desktop publishes the local SSH details, point your editor at them.

## Works with or without an Agent

The desktop is equally useful as a clean development surface for human-driven work — open as many isolated environments as your machine can host, develop in any one, and let ERun handle the infrastructure underneath.

## Side by side with an Agent

By default, **the Agent runs inside the env** — the runtime pod ships the configured Agent's CLI (`claude`, `codex`, …) pre-wired against the in-pod MCP loopback. The desktop's AI panel surfaces it; any terminal inside the pod can launch it directly. If the Agent ends — you exit it, it crashes, or the container's memory limit kills it — the AI tab says so instead of silently turning into a shell: it names the exit (a memory kill includes the hint to raise Memory in the env's Runtime settings) and prints the exact command to resume the conversation.

For a Claude env, the AI tab of the env settings dialog carries the Claude launch controls. **Effort** sets how hard Claude works per turn: the levels from low through max trade response time for thinking depth, and **ultracode** — the default for new envs — runs at xhigh thinking effort and additionally turns on standing multi-agent workflow orchestration, so Claude can fan work out across coordinated agents without being asked. Pick a plain level (for example max) when you want pure single-agent thinking instead. **Default model** picks the model the env's Claude session starts on, chosen from the environment's available models — tick `fable` under Available models to make it selectable. Left on **Default** it starts the first of the env's available models (`opus` by default), so a session never lands on an unavailable model; `fable` is never chosen automatically. **Launch Claude in verbose + debug mode** streams Claude's own diagnostics into the AI tab. The desktop applies your choices when it opens the env's Claude session, and saving a changed launch setting reopens the env's open AI tabs so it takes effect immediately — the Claude conversation resumes where it left off. For the exact levels and how the values resolve, see [Agent reference · Configuration](/reference/configuration).

The AI tab's Claude session also has **Remote Control on by default**, so you can watch and steer it from the Claude iOS app (or claude.ai/code) while you're away from the desktop: sign into the app with the same Claude account and the running session appears in its Code tab named `<tenant>/<env>`. Pairing rides your claude.ai login — the same one the session prompts you for when it first starts — so it needs a Claude subscription plan, and it's turned off automatically for environments whose Claude runs through the Bedrock or Mantle gateways, which the pairing relay can't sign into.

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
