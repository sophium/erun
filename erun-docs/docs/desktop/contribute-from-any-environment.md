---
title: Contribute to ERun from any environment
---

# Contribute to ERun from any environment

The titlebar's **Contribute** toggle (the git-fork icon) lets you patch ERun itself without leaving the env you're working in. It is available for any non-`erun` agent env (local-agent or remote-agent); it is hidden for runtime envs and for the special `erun` tenant.

When you toggle Contribute on, the desktop:

1. Clones the ERun source into `~/git/erun` inside the env (the host for a local-agent env, the runtime pod for a remote-agent env). The clone is idempotent — a checkout that already points at the canonical ERun remote is reused as-is.
2. Installs a small shim at `~/.erun/contribute/bin/erun` that forwards every invocation to the clone's `erun-cli/run.sh`. Subsequent `erun` calls inside the contribute tabs — whether typed at the prompt or spawned as child processes by an Agent — go through that script, which rebuilds the binary from the current source on each run.
3. Opens two extra tabs alongside the env's normal ERun + AI + Local tabs: **ERun (contribute)** is a shell pointing at the clone; **AI (contribute)** is the Agent attached to the clone. Both tabs are marked with the git-fork icon in the tab strip so you never confuse a contribute terminal with the env's own.
4. Adds an **Env / ERun** segmented control in the review panel's changed-files sidebar. Flipping it switches what the diff view shows: the env's worktree or the ERun clone's worktree, side by side as you iterate.

To try your changes to the desktop app itself, click the **Open contribute app** button (external-link icon) that appears next to the Contribute toggle while contribute mode is on. The desktop boots `erun app --headless` inside the contribute terminal — building the binary on the fly from your clone — brings up a port-forward from the env's contribute-app port out to your host, and opens the locally-built desktop app in a new browser tab. Subsequent code edits are picked up on the next rebuild (Ctrl-C the running process in the ERun (contribute) tab and click the launcher again).

Toggle the switch off when you're done. The contribute tabs close, the headless app and its port-forward shut down, the diff view reverts to the env, and the clone stays on disk so re-enabling later is instant.

## Where next

- [`erun contribute`](/cli/contribute) — the same clone-and-shim flow from the CLI.
- [Reviews](/desktop/reviews) — proposing the change you make in the contribute tabs.
