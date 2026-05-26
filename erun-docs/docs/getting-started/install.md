---
title: Install
---

# Install ERun

Install ERun through your platform's package manager. One command gives you both the **CLI** and the **desktop app**.

## Prerequisites

On your machine:

- **`git`** — ERun resolves the project root by walking up to find a `.git` directory; every tenant maps to one git repository.
- **A Kubernetes cluster.** You pick where it lives:
  - **Local.** Enable Kubernetes in your favourite Docker tool — Docker Desktop, OrbStack, Rancher Desktop. That's it; ERun takes it from there.
  - **Cloud.** No prerequisites — ERun provisions a minimal, secure Kubernetes cluster for you on demand.

You don't have to pick local vs cloud upfront. The same workflow targets both — local while your machine has capacity, cloud when you need more.

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

## Next

[Create your first environment →](/getting-started/first-environment)
