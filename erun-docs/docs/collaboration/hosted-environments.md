---
title: Managing hosted environments
---

# Managing hosted environments

A hosted erun platform gives your tenant its own environments over an API — create one, deploy it, stop it when idle, delete it when you're done — the same lifecycle [`erun open`](/cli/open)/[`erun stop`](/cli/stop)/[`erun delete`](/cli/delete) give a local or cloud-context environment, driven instead through [`erun platform`](/cli/platform) once you're signed in.

For the full concept and spec, see [Agent reference · Hosted platform](/concepts/hosted-platform).

## Sign in once

```bash
erun cloud init erun --api-url https://api.<tenant>.services.<your-domain>
erun cloud login --alias erun+api.<tenant>.services.<your-domain>@erun
```

`login` opens a device-code sign-in (or a browser tab if your issuer has no device flow) and confirms you're in by printing your tenant.

## Create and deploy an environment

```bash
erun platform env register --name prod --type runtime --runtime-version 1.4.2
```

The platform starts a server-side deploy immediately; poll its status until it settles:

```bash
erun platform env get <environment-id>
```

`status` moves `registered` → `provisioning` → `running` (or `failed`, with a `provisionError` explaining why). A `running` environment is already reachable at its MCP hostname — the platform wires exposure (DNS + Ingress) into the same deploy, so there is no separate step to run once status settles. See [Hosted platform · Automatic exposure](/concepts/hosted-platform#automatic-exposure) for when that wiring runs and how it fails safely on a platform not configured for it.

New to erun and haven't run `erun push` yet? Registering an environment still works: it bootstraps on the canonical ERun runtime image instead of a project image you haven't published. Once you publish your own `<tenant>-devops` image at a version, deploying that version gets your own image and plan instead. See [Hosted platform · Provisioning lifecycle](/concepts/hosted-platform#provisioning-lifecycle) for the mechanism.

Re-deploy at a different version later with:

```bash
erun platform env deploy <environment-id> --version 1.5.0
```

## Preview before you commit

`erun platform provision` resolves the full plan — quota, placement, namespace, and deploy — without creating anything, so you can check it before registering for real:

```bash
erun platform provision --env-name staging --env-type runtime
```

## Stop and delete

```bash
erun platform env stop <environment-id>     # scale to zero; state survives
erun platform env delete <environment-id>   # tear down the namespace; irreversible
```

## Where an environment lands

Name a cloud context you've already registered with `contextId` and a hosted runtime environment deploys there instead of the platform's own cluster; leave it unset and the platform auto-selects one of your own registered contexts with room, or falls back to its own cluster if you haven't registered any. See [Placement](/concepts/hosted-platform#single-cluster-placement) for the full decision and what an unresolvable request looks like (a clear, immediate error rather than a silently-wrong deploy).

## Quotas

Your tenant has a cap on how many environments it may register at once. `erun platform env register` reports a clear conflict at the cap; `erun platform provision` shows you the same quota decision in its preview before you commit.

Each of your environments also runs inside a namespace capped on CPU, memory, and storage — enforced by Kubernetes itself, not just recorded. On top of that, your tenant has an aggregate CPU/memory/storage budget across all of your runtime environments combined: registering (or redeploying) one that would push your total past that budget is refused the same way, naming which resource and by how much. If your platform operator has set either cap unusually low, registering a new runtime environment is refused with a clear conflict naming the cap, rather than succeeding and failing to actually come up.

You can see your own tenant's full quota — the environment-count cap, the per-environment ceiling, and the aggregate budget — at any time via the API's `GET /v1/quota` (no operator role required); there is no CLI command for it yet. All caps are set by your platform operator (operations-only); reach out to them to raise any of them. See [Hosted platform · Quotas](/concepts/hosted-platform#quotas) for the full spec.

## Where next

- [`erun platform` CLI reference](/cli/platform) — every subcommand and flag.
- [Agent reference · Hosted platform](/concepts/hosted-platform) — the full lifecycle, placement, and RBAC spec.
- [Cloud contexts](/concepts/cloud-contexts) — the cluster model a future multi-cluster placement will build on.
