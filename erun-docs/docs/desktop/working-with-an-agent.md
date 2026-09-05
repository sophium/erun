---
title: Working with an Agent
---

# Working with an Agent

## Works with or without an Agent

The desktop is equally useful as a clean development surface for human-driven work — open as many isolated environments as your machine can host, develop in any one, and let ERun handle the infrastructure underneath.

## Side by side with an Agent

By default, **the Agent runs inside the env** — the runtime pod ships the configured Agent's CLI (`claude`, `codex`, …) pre-wired against the in-pod MCP loopback. The desktop's AI panel surfaces it; any terminal inside the pod can launch it directly. If the Agent ends — you exit it, it crashes, or the container's memory limit kills it — the AI tab says so instead of silently turning into a shell: it names the exit (a memory kill includes the hint to raise Memory in the env's Runtime settings) and prints the exact command to resume the conversation.

Opening the AI tab checks whether the environment is already held by another job — one started by an orchestrator, another desktop, or the CLI. If it is, the desktop names the job and asks before starting a second Agent alongside it, since the two would compete for the same pod's CPU, memory and disk. Starting anyway is a deliberate choice, not a blocked one — there are good reasons to run two (a long build in one tab, a quick question in the other) — and a small persistent reminder stays on the AI tab for as long as the other job keeps running.

### Claude launch controls

For a Claude env, the AI tab of the env settings dialog carries the Claude launch controls. **Effort** sets how hard Claude works per turn: the levels from low through max trade response time for thinking depth, and **ultracode** — the default for new envs — runs at xhigh thinking effort and additionally turns on standing multi-agent workflow orchestration, so Claude can fan work out across coordinated agents without being asked. Pick a plain level (for example max) when you want pure single-agent thinking instead. **Default model** picks the model the env's Claude session starts on, chosen from the environment's available models — tick `fable` under Available models to make it selectable. Left on **Default** it starts the first of the env's available models (`opus` by default), so a session never lands on an unavailable model; `fable` is never chosen automatically. **Launch Claude in verbose + debug mode** streams Claude's own diagnostics into the AI tab. The desktop applies your choices when it opens the env's Claude session, and saving a changed launch setting reopens the env's open AI tabs so it takes effect immediately — the Claude conversation resumes where it left off. For the exact levels and how the values resolve, see [Agent reference · Configuration](/reference/configuration).

### Remote Control

The AI tab's Claude session also has **Remote Control on by default**, so you can watch and steer it from the Claude iOS app (or claude.ai/code) while you're away from the desktop: sign into the app with the same Claude account and the running session appears in its Code tab named `<tenant>/<env>`. Pairing rides your claude.ai login — the same one the session prompts you for when it first starts — so it needs a Claude subscription plan, and it's turned off automatically for environments whose Claude runs through the Bedrock or Mantle gateways, which the pairing relay can't sign into.

### Bringing your own Agent

When you do want a laptop-side Agent in addition, the env has two endpoints on the runtime pod — SSH and [MCP](/mcp/overview) — and both accept any client.

- IDEs (VS Code, IntelliJ, Cursor, Zed, …) attach over SSH.
- **The Claude Code desktop app and Codex desktop app attach the same way** — they open the env as a remote workspace, edit files, run commands. They also use MCP for structured ERun operations (`idle`, `list`, `doctor`, `build`, …).
- Custom agents (any MCP client) typically stick to MCP for structured calls and reach for SSH only when they need shell access.

A commit you make in VS Code is immediately visible to the Agent's next file read. An action the Agent takes shows up in your terminal scrollback and in the [audit trail](/collaboration/operator-in-the-loop). No parallel worlds.

## Where next

- [Terminals and editors](/desktop/terminals-and-editors) — the same SSH endpoint, from your side of the terminal.
- [Reference · Configuration](/reference/configuration) — the exact Claude effort levels and how they resolve.
- [MCP protocol + tools](/mcp/overview) — the tool schema behind structured ERun operations.
