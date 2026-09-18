---
title: Command primitives
---

# Command primitives

ERun's delivery commands are **pure primitives**: `build`, `push`, `deploy`, and `open` each do exactly one thing, and the thing that flows between them is a **version**. Understanding that split is what makes the [pipeline](/pipeline) predictable — every command is safe to run on its own, and orchestration is something a caller composes on top, not magic hidden inside a command.

## One job each

Each primitive has a single responsibility and contains no environment-type or environment-name decision logic:

- **[`build`](/cli/build)** — builds the container images and **mints the version**. By default it mints a snapshot (`<base>-snapshot-<timestamp>`); `--release` (or an explicit version) pins a bare semver instead. `build` is the *only* command that creates a version.
- **[`push`](/cli/push)** — publishes a version's outputs: the multi-arch image **and** the runtime helm chart, together at that version. It takes the version as input; it never mints one.
- **[`deploy`](/cli/deploy)** — installs a published version into an environment, by reference. It never builds or pushes. A version is required.
- **[`open`](/cli/open)** — opens a shell to the environment. It doesn't build or deploy.

The key idea: **a version is a content identity, minted once by `build`.** `push` and `deploy` require you to say *which* version — running them without one is an error, not a signal to go build something.

## The version threads through

When you ship a change, the version that `build` mints is the same value `push` publishes and `deploy` installs. Nothing re-derives it; it's passed along.

```bash
erun build --output json     # mints a version, prints {version, baseVersion, images}
erun push --version <version>   # publishes the image + chart at that version
erun deploy <env> --version <version>   # installs that exact version
```

That's why `push` and `deploy` ask for the version explicitly — so what you deploy is always the precise thing you built, with no chance of a silent rebuild or an overwrite of a published artifact.

## Orchestration vs convenience switches

You usually don't type those three commands by hand. There are two ways they get composed — and the difference matters:

- **Operator convenience switches.** At the terminal, `erun build --deploy` runs build → push → deploy in one go, `erun build --release` folds in the release flow, `erun build --e2e` (implies `--deploy`) also runs [`erun e2e`](/cli/e2e) against the environment just deployed, `erun push --build` builds the current source then publishes the version it mints, and `erun open --deploy` deploys the runtime before dropping you into the shell. These are shortcuts *for a human* — they compose the primitives for you and thread the version automatically.
- **Programmatic orchestration.** The desktop app, scripts, and Agents driving [MCP](/mcp/overview) do **not** use those switches. They run the primitives themselves — `build`, then `push`, then `deploy` — capturing the version from `erun build --output json` and threading it through. This keeps the policy ("for this environment, the operator's click means build → push → deploy") in the caller, where it belongs, instead of buried inside a command.

So the desktop app's **Deploy** button isn't calling one clever command that decides everything — it's running the same plain primitives you could run yourself, in the order that fits the environment, with the version carried between them.

## Why pure primitives

Keeping each command to one job pays off in three ways:

- **Deploy can't surprise you.** Because `deploy` only installs a published version by reference, it can never rebuild your working tree or overwrite a published artifact. What you ask for is what rolls out.
- **Any pushed version is deployable.** `push` publishes the chart alongside the image for *every* version — snapshot or release — so there's no gap where an image exists without a chart to install it.
- **The same flow works everywhere.** No command branches on environment type, so a snapshot to a throwaway env and a release to production take the identical steps; only the version and the target differ.

## Where next

- **[Delivery pipeline](/pipeline)** — how the primitives line up into `build → push → deploy`.
- **[Versioning](/versioning)** — how the version `build` mints is generated.
- **[CLI flag spec](/agent-reference/cli-flags)** — the full flag and error contract for each primitive, including `--output json` and the version-required rules.
