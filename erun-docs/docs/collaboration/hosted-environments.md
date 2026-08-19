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

`status` moves `registered` → `provisioning` → `running` (or `failed`, with a `provisionError` explaining why). A `running` environment is already reachable at its MCP hostname — the platform wires exposure (DNS + Ingress) into the same deploy, so there is no separate step to run once status settles. See [Hosted platform · Automatic exposure](/concepts/hosted-platform#automatic-exposure) for when that wiring runs and how it fails safely on a platform not configured for it. Re-deploy at a different version later with:

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

Today every hosted runtime environment deploys into the platform's own cluster — there is no way yet to point one at a cloud context you've bootstrapped yourself. See [Single-cluster placement](/concepts/hosted-platform#single-cluster-placement) for why, and what a request to do otherwise looks like (a clear, immediate error rather than a silently-wrong deploy).

## Quotas

Your tenant has a cap on how many environments it may register at once. `erun platform env register` reports a clear conflict at the cap; `erun platform provision` shows you the same quota decision in its preview before you commit.

## Where next

- [`erun platform` CLI reference](/cli/platform) — every subcommand and flag.
- [Agent reference · Hosted platform](/concepts/hosted-platform) — the full lifecycle, placement, and RBAC spec.
- [Cloud contexts](/concepts/cloud-contexts) — the cluster model a future multi-cluster placement will build on.
