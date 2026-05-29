---
title: Install
---

# Install ERun

Install ERun through your platform's package manager. One command gives you both the **CLI** and the **desktop app**.

## Prerequisites

On your machine:

- **`git`** — ERun resolves the project root by walking up to find a `.git` directory; every tenant maps to one git repository.
- **A Kubernetes cluster.** You pick where it lives:
  - **Local.** Enable Kubernetes in your favourite Docker tool — Docker Desktop, OrbStack, Rancher Desktop. ERun takes it from there. **Recommended on macOS and Linux.**
  - **Cloud.** No prerequisites — ERun provisions a minimal, secure Kubernetes cluster for you on demand. **Recommended on Windows** (local Kubernetes on Windows runs through WSL2 / Hyper-V and Docker Desktop bind mounts, all of which add friction the cloud path skips).

You don't have to commit upfront. The same workflow targets both — switch a project from a local cluster to a cloud one (or back) without rebuilding anything.

## macOS — Homebrew

```bash
brew tap sophium/erun https://github.com/sophium/erun
brew install erun
```

## Windows — Scoop

```powershell
scoop bucket add erun https://github.com/sophium/erun
scoop install erun
```

Either install puts the `erun` CLI on your `PATH` and installs the desktop application alongside it.

## Optional: install the ERun plugin in your Claude Code

Once `erun` is installed, you can add the ERun plugin to your laptop Claude Code so the same issue-filing and contribution skills your env already has are available locally:

```bash
/plugin marketplace add sophium/erun
/plugin install erun-tools@sophium/erun
```

The plugin gives Claude Code four skills: `erun-file-issue`, `erun-contribute`, `erun-blueprint-rls-db`, and `erun-blueprint-api`. See [Skills](/collaboration/skills) for what each one does and when to use it.

Codex doesn't have an analogous marketplace yet; inside a deployed env you get the skills automatically.

## Next

[Create your first environment →](/getting-started/first-environment)
