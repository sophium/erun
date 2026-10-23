---
title: Install
---

# Install ERun

Install ERun through your platform's package manager. On macOS and Windows one command gives you both the **CLI** and the **desktop app**; on Linux they are two Debian packages.

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

## Linux — Debian packages

Each [release](https://github.com/sophium/erun/releases) carries two `.deb` files per architecture (`amd64`, `arm64`):

| Package | File | Contents |
|---|---|---|
| `erun` | `erun_<version>_<arch>.deb` | The `erun` CLI, with the `emcp` and `eapi` executables. |
| `erun-desktop` | `erun-desktop_<version>_<arch>.deb` | The `erun-app` desktop application, its menu entry and icon. |

Download both from the release and install them with `apt`, which pulls in the libraries the desktop app needs (GTK 3, WebKitGTK 4.1):

```bash
version=1.0.300   # the release you want
arch=$(dpkg --print-architecture)
curl -fsSLO "https://github.com/sophium/erun/releases/download/v${version}/erun_${version}_${arch}.deb"
curl -fsSLO "https://github.com/sophium/erun/releases/download/v${version}/erun-desktop_${version}_${arch}.deb"
sudo apt install "./erun_${version}_${arch}.deb" "./erun-desktop_${version}_${arch}.deb"
```

The CLI package runs on any Debian or Ubuntu release. The desktop package names its libraries as Debian 13 and Ubuntu 24.04 do, so it installs there and on later releases. Install only `erun` when you don't want the desktop app.

## Optional: install the ERun plugin in your Claude Code

Once `erun` is installed, you can add the ERun plugin to your laptop Claude Code so the same issue-filing and contribution skills your env already has are available locally:

```bash
/plugin marketplace add sophium/erun
/plugin install erun-tools@sophium/erun
```

The plugin gives Claude Code the same erun skills your env has — issue-filing, contribution, and the blueprint skills for building and deploying. See [Skills](/collaboration/skills) for what each one does and when to use it.

Codex doesn't have an analogous marketplace yet; inside a deployed env you get the skills automatically.

## Next

[Create your first environment →](/getting-started/first-environment)
