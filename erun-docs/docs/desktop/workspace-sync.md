---
title: Workspace sync
---

# Workspace sync (optional)

For a **remote-agent** env, the worktree lives in the pod. Workspace sync mirrors it **down to a folder on your machine** so you can review and act on it host-side — open it in a host IDE, read the synced files, and run binaries the Agent built in the pod — without reaching into the pod for every step. It's the piece that makes an agent-driven env comfortable on Windows, where the pod-side worktree isn't directly openable by a host editor.

Enable it from the desktop's env settings panel and pick the local folder to mirror into. The sync is **one-way, pod → host**: the desktop keeps the folder matching the pod's worktree (tracked and untracked-but-not-ignored files), so **treat it as read-only** — host edits are overwritten on the next pass. Files removed in the pod are removed from the mirror too. The mirror is a **plain directory** — it needs no local git; to see the Agent's uncommitted diff, view it from the pod (the desktop's review, or ask the in-pod Agent to run `git diff`).

### Build artifacts

Build artifacts land alongside it. Anything the Agent writes to the pod's outputs directory — a cross-compiled `.exe`, a report, a bundle — is mirrored into a **read-only `.erun-outputs` folder** next to the synced source. That's how a Windows binary built in the Linux pod reaches your machine: it syncs down, and you run or debug it from the desktop (the same artifacts are also available via **Download** on the env's Outputs dialog). On a Mac, an arriving macOS binary is signed and made runnable on the way in — macOS kills an unsigned one on launch without saying so — and the desktop posts a notification naming the signer it used, or why it could not sign. See [`erun outputs`](/cli/outputs#macos-binaries-arrive-runnable) for the exact rule.

For the polling cadence, the cleanup semantics, and the size budget, see [Agent reference · Workspace sync spec](/agent-reference/workspace-sync-spec).

## Where next

- [Terminals and editors](/desktop/terminals-and-editors) — how clickable file paths resolve against the synced mirror.
- [`erun sshd sync`](/cli/sshd) — the same sync from the CLI.
