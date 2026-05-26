---
title: CLI overview
---

# CLI overview

The `erun` CLI is ERun's **automation surface**. It's what CI/CD pipelines invoke for `build` / `push` / `deploy`, what runs inside every runtime pod, and what the desktop app shells out to when you click a button. If you're scripting, running on a build runner, or working headlessly, the CLI is your interface.

For day-to-day human use on macOS or Windows, use the [desktop app](/desktop/overview) — it's the same CLI underneath, with a UI on top.

<figure className="erun-hero-figure">
  <img src="/img/cli-lifecycle.svg" alt="CLI lifecycle. Five main commands in a horizontal flow connected by arrows: erun init (create tenant + env, first time) → erun open (attach to env, SSH + MCP ready) → erun build (in an agent env, snapshot or release) → erun deploy (build · push · roll out, helm upgrade) → erun delete (tear down env, namespace + config). Below, a row of inspection and automation commands: erun list (tenants · envs · target), erun doctor (diagnose env), erun mcp (server for agents), erun release (CI publishes), erun version (build info). A strapline notes all commands support --dry-run for preview before any side effect." />
  <figcaption>One straight path from `init` to `delete`. The bottom row holds inspection and automation commands you reach for as needed.</figcaption>
</figure>

Every command supports `--help` and `--dry-run`.

## Commands

| Command | What it does |
|---|---|
| `erun` | Universal entry point. In a fresh project, runs `init`. In a configured one, runs `open`. |
| [`erun init`](/cli/init) | Create or update a tenant + environment, bring the environment up. |
| [`erun open`](/cli/open) | Open a shell into the environment (brings it up if missing). |
| [`erun add`](/cli/add) | Scaffold a conventional component (source + Dockerfile + chart + deploy-plan entry) from a built-in template. |
| [`erun list`](/cli/list) | List tenants, environments, status, and effective target. |
| [`erun build`](/cli/build) | Build the project's Docker images (agent envs only — runtime envs receive deploys, they don't build). |
| [`erun push`](/cli/push) | Push built images to the configured container registry. |
| [`erun deploy`](/cli/deploy) | Build → push → roll out the helm chart. |
| [`erun doctor`](/cli/doctor) | Inspect the local config or a running environment for problems. |
| [`erun mcp`](/cli/mcp) | Launch the MCP server (used by IDEs and AI tooling). |
| [`erun release`](/cli/release) | Plan and execute a project release. |
| [`erun delete`](/cli/delete) | Remove an environment's namespace and local config. |
| [`erun version`](/cli/version) | Print build version + commit. |

## Dry-run and verbosity

Every action-oriented command supports `--dry-run` — resolve and print every step without performing side effects. The trace is byte-for-byte identical to the real-run trace except that values matching common secret patterns (tokens, JWTs, AWS keys, GitHub tokens, kubeconfig user tokens, SSH private keys) are replaced with `<redacted…>` placeholders, so previews are safe to paste into a PR or chat. For the exact regex set and emit-time semantics, see [Agent reference · Dry-run redaction](/agent-reference/dry-run-redaction).

`-v` / `--verbose` streams external tool output; `-vv` adds per-command trace lines for every action and decision; `--time` prints elapsed wall time at the end. Full flag set per command is on the [CLI flag spec](/agent-reference/cli-flags) page.

## How arguments resolve

Most commands accept `[TENANT] [ENVIRONMENT]` as positional args. When omitted, ERun resolves them from explicit flags, then the current directory's git repo, then user-level defaults, then prompt. See [Configuration · Effective tenant + environment](/reference/configuration#effective-tenant--environment-for-a-cli-command) for the full algorithm.
