---
title: CLI overview
---

# CLI overview

The `erun` CLI is ERun's **automation surface**. It's what CI/CD pipelines invoke for `build` / `push` / `deploy`, what runs inside every runtime pod, and what the desktop app shells out to when you click a button. If you're scripting, running on a build runner, or working headlessly, the CLI is your interface.

For day-to-day work on macOS or Windows, use the [desktop app](/desktop/overview) — it's the same CLI underneath, with a UI on top.

<figure className="erun-hero-figure">
  <img src="/img/cli-lifecycle.svg" alt="CLI lifecycle. Five main commands in a horizontal flow connected by arrows: erun init (create tenant + env, first time) → erun open (attach to env, SSH + MCP ready) → erun build (in an agent env, snapshot or release) → erun deploy (build · push · roll out, helm upgrade) → erun delete (tear down env, namespace + config). Below, a row of inspection and automation commands: erun list (tenants · envs · target), erun doctor (diagnose env), erun mcp (server for agents), erun release (CI publishes), erun version (build info). A strapline notes all commands support --dry-run for preview before any side effect." />
  <figcaption>One straight path from `init` to `delete`. The bottom row holds inspection and automation commands you reach for as needed.</figcaption>
</figure>

Every command supports `--help`. Action-oriented commands also support `--dry-run` (read-only commands like `list` and `idle` don't need it).

The `build → release → push → deploy` commands form ERun's delivery pipeline. See the [Delivery pipeline](/pipeline) for how they fit together.

## Commands

| Command | What it does |
|---|---|
| `erun` | Universal entry point. In a fresh project, runs `init`. In a configured one, runs `open`. |
| [`erun init`](/cli/init) | Create or update a tenant + environment, bring the environment up. |
| [`erun open`](/cli/open) | Open a shell into the environment (brings it up first if needed). |
| [`erun delete`](/cli/delete) | Tear down an environment — remote namespace + local config. |
| [`erun build`](/cli/build) | Build the project's container images (agent envs only — runtime envs receive deploys). |
| [`erun release`](/cli/release) | Cut a release: stamp the version, tag it, push the tag. |
| [`erun push`](/cli/push) | Push built images to the configured container registry. |
| [`erun deploy`](/cli/deploy) | Roll a version out to an environment (build · push · helm upgrade). |
| [`erun publish`](/cli/publish) | Mirror a built version's images to the shared registry (`from`→`to`), no build or deploy. |
| [`erun expose`](/cli/expose) | Expose an env's Service at a public hostname under the platform's services zone. |
| [`erun cloud`](/cli/cloud) | Set up and manage cloud provider aliases (AWS SSO). |
| [`erun context`](/cli/context) | Create and power managed cloud contexts (an EC2 instance running k3s). |
| [`erun sshd`](/cli/sshd) | Enable SSH (and IDE) access to a remote environment. |
| [`erun list`](/cli/list) | List tenants, environments, and the effective target. |
| [`erun idle`](/cli/idle) | Show an environment's idle / auto-stop status. |
| [`erun doctor`](/cli/doctor) | Diagnose and repair an environment's runtime and config. |
| [`erun version`](/cli/version) | Print erun's build version (and the project's, when in one) + latest published versions. |
| [`erun mcp`](/cli/mcp) | Run the MCP server for Agents (launches `emcp`); `call` / `tools` / `token` reach an env's MCP edge. |
| [`erun api`](/cli/api) | Run the backend API server (launches `eapi`). |
| [`erun app`](/cli/app) | Launch the desktop app. |
| [`erun exec`](/cli/exec) | Run repository helpers — `diff`, `raw`. |
| [`erun contribute`](/cli/contribute) | Contribute-mode helpers — clone the ERun repo locally. |

## Dry-run and verbosity

Every action-oriented command supports `--dry-run` — resolve and print every step without performing side effects. The trace is byte-for-byte identical to the real-run trace except that values matching common secret patterns (tokens, JWTs, AWS keys, GitHub tokens, kubeconfig user tokens, SSH private keys) are replaced with `<redacted…>` placeholders, so previews are safe to paste into a PR or chat. For the exact regex set and emit-time semantics, see [Agent reference · Dry-run redaction](/agent-reference/dry-run-redaction).

`-v` / `--verbose` streams external tool output; `-vv` adds per-command trace lines for every action and decision; `--time` prints elapsed wall time at the end. Full flag set per command is on the [CLI flag spec](/agent-reference/cli-flags) page.

You never have to re-run a failed command just to capture diagnostics: every environment-scoped command (`open`, `doctor`, `deploy`, a scoped `upgrade`) automatically appends its full trace to the environment's rolling [trace log](/reference/config-locations#trace-log), readable at any time from the [desktop's Diagnostics console](/desktop/overview#diagnostics-console) — including for runs that finished before you went looking. `--dry-run` previews are never written to it.

## How arguments resolve

Most commands accept `[TENANT] [ENVIRONMENT]` as positional args. When omitted, ERun resolves them from explicit flags, then the current directory's git repo, then user-level defaults, then prompt. See [Configuration · Effective tenant + environment](/reference/configuration#effective-tenant--environment-for-a-cli-command) for the full algorithm.
