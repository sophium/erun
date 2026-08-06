---
title: erun stop
---

# `erun stop`

Stop an environment so its cluster capacity goes back to the environments you are actually using. Opening the environment again starts it.

Most environments are idle most of the time, but an idle environment still reserves everything it was given: the runtime container's CPU and memory limits, plus whatever its Docker daemon sidecar is really consuming — and that sidecar has no limit at all, so a Testcontainers run or a warm buildkit cache can hold gigabytes the cluster cannot see coming. Stack four environments on one machine and the node is over-committed on behalf of environments nobody is using. `erun stop` is how you get that back.

## Synopsis

```
erun stop [TENANT] [ENVIRONMENT] [flags]
```

Arguments resolve the same way as [`erun open`](/cli/open): from working directory, then defaults.

## Flags

| Flag | Description |
|---|---|
| `--tenant <name>` | Stop a specific tenant. |
| `--environment <name>` | Stop a specific environment. |
| `--dry-run` | Show every action that would be performed without executing. |
| `--output json` | Emit the structured result on stdout for orchestrators. |

## What survives a stop

Everything you would not want to rebuild:

| Kept | Why it matters |
|---|---|
| The `/home/erun` volume | Your workspace, shell history, agent config, [outputs](/cli/outputs), cloud credentials. |
| The Docker volume | The image store and the build cache, so the next build is still warm. |
| A builds-here environment's worktree | It is mounted from your machine and is never touched. |

What does not survive: **whatever was running in the pod**. A build, an agent session, a long job — stopping ends it. Stop an environment because nobody is using it, not to pause work in progress.

## Waking it again

There is one way to start an environment: **open it**.

```bash
erun open my-tenant rihards-dev
```

[`erun open`](/cli/open) scales the runtime back up and waits for it before it does anything else, because a port-forward cannot attach to an environment with no pod. That is also how the desktop app and a host-side orchestrator wake an environment they find stopped — nothing inside the pod is involved, which is the point: a stopped environment has no pod to ask.

[`erun deploy`](/cli/deploy) deliberately does **not** wake an environment. Deploy installs a version; it does not decide whether the environment should be running. A stopped environment that you deploy stays stopped, and the newly installed version is what starts when you next open it.

## Examples

Preview the stop:

```bash
erun stop my-tenant rihards-dev --dry-run
```

Stop the environment in the current project scope:

```bash
erun stop
```

Stop an environment from a script and read the result:

```bash
erun stop my-tenant rihards-dev --output json
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant + environment not configured. | Errors with "no such environment"; nothing is touched. Exit `1`. |
| The environment's runtime is not deployed. | Errors with "runtime for `<tenant>/<env>` is not deployed … nothing to stop"; nothing is touched. Exit `1`. Deploy it first, or there is nothing holding capacity. |
| The environment is already stopped. | Succeeds and says so; the stop is recorded if it was not already. Exit `0`. |
| Cluster unreachable. | Errors with the cluster's message; nothing is changed. Exit `1`. `erun stop` never starts a stopped [cloud context](/concepts/cloud-contexts) to reach it — starting a machine in order to stop a pod on it is the opposite of what you asked for. |

Full flag, result-shape and error-code spec: [Agent reference · CLI flag spec · `erun stop`](/agent-reference/cli-flags#erun-stop).

## Where next

- [`erun open`](/cli/open) — what starts a stopped environment.
- [Inside an environment](/concepts/runtime-pods) — what an environment reserves while it runs, and how many fit on one machine.
- [Cloud contexts](/concepts/cloud-contexts) — stopping the whole machine an environment runs on, rather than one environment.
