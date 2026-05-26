---
title: First environment
---

# Create your first environment

In your project folder:

```bash
erun
```

That's it. The first time, ERun walks you through a short setup. After that, the same command picks up where you left off.

<figure className="erun-hero-figure">
  <img src="/img/first-run.svg" alt="First run flow. Three steps left to right: '$ erun' typed in your project folder (charcoal box), 'ERun sets up the env' (cyan, with note 'walks you through it on the first run'), 'Ready to work' (cyan, with note 'open in your IDE · the Agent is there'). A strapline reads: 'Subsequent runs skip the setup.'" />
  <figcaption>One command, env ready. Subsequent runs jump straight to ready.</figcaption>
</figure>

The desktop app does the same thing through its UI — add the project, fill in the same fields, save.

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
