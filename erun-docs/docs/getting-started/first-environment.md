---
title: First environment
---

# Create your first environment

The recommended path depends on your OS.

## macOS — `erun` from your project folder

In your project folder:

```bash
erun
```

That's it. The first time, ERun walks you through a short setup. After that, the same command picks up where you left off. The default env runs against the Kubernetes you've enabled in Docker Desktop / OrbStack / Rancher Desktop — locally on your laptop.

<figure className="erun-hero-figure">
  <img src="/img/first-run.svg" alt="First run flow. Three steps left to right: '$ erun' typed in your project folder (charcoal box), 'ERun sets up the env' (cyan, with note 'walks you through it on the first run'), 'Ready to work' (cyan, with note 'open in your IDE · the Agent is there'). A strapline reads: 'Subsequent runs skip the setup.'" />
  <figcaption>One command, env ready. Subsequent runs jump straight to ready.</figcaption>
</figure>

The desktop app does the same thing through its UI — add the project, fill in the same fields, save.

## Windows — desktop app + a remote env

On Windows, **open the ERun desktop app** and create a **remote env**. The desktop walks you through picking the project repo and the cloud context; ERun provisions a managed Kubernetes cluster and brings the runtime pod up on it. Your IDE and the in-pod Agent attach over the desktop's port-forwards.

We don't recommend local Kubernetes on Windows for day-to-day work — `hostPath` mounts, the WSL2 / Hyper-V layering, and Docker Desktop's bind semantics all add friction the remote-env path skips entirely. The same Agent + IDE + tooling experience works either way; the remote env just sidesteps the local-cluster headaches.

## Work in it

Once the env is ready, you can:

- **Open it in your IDE.** Click the env in the desktop sidebar and pick VS Code or IntelliJ; any SSH-aware editor (Cursor, Zed, JetBrains products, Neovim) attaches too. From the CLI: `erun open --vscode` or `--intellij`.
- **Work with the Agent.** Claude Code, Codex, or your configured Agent is already in the env — the desktop's AI panel surfaces it; a terminal inside the env can launch it directly.
- **Type in the env's terminal.** The desktop publishes one per env.

Operator and Agent are in the same env, looking at the same files in real time. You can take over, hand back, or work alongside the Agent at any time.

Open as many environments in parallel as your machine can host. ERun handles the details — see [Inside an environment](/concepts/runtime-pods) if you ever want to look under the hood, but you don't need to.

## Where next

- **[Three scenarios](/getting-started/three-scenarios)** — peer review, hotfix, CI wait — solved.
- [Tenants and environments](/concepts/tenants-and-environments) — concepts in depth.
- [Environment types](/concepts/environment-types) — local-agent, remote-agent, runtime.
- [Cheatsheet](/reference/cheatsheet) — common commands.
