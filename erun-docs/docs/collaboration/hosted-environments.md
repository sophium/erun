---
title: Managing hosted environments
---

# Managing hosted environments

A hosted erun platform gives your tenant its own environments over an API — create one, deploy it, stop it when idle, delete it when you're done — the same lifecycle [`erun open`](/cli/open)/[`erun stop`](/cli/stop)/[`erun delete`](/cli/delete) give a local or cloud-context environment, driven instead through [`erun platform`](/cli/platform) once you're signed in.

For the full concept and spec, see [Agent reference · Hosted platform](/concepts/hosted-platform).

The examples below use the CLI, but every action — previewing, registering, deploying, stopping, deleting — has an equivalent in the desktop app's tenant dashboard, on its **Registration** tab. Open the tenant dashboard and switch to Registration to see what's already registered on the platform, alongside the local tenant and environments the sidebar already shows: those are two separate objects, and creating one does not create the other. Registering a new tenant, or enrolling its first user, still needs `erun platform tenant create`/`erun platform user enroll` from the CLI or console — the Registration tab points there rather than half-configuring it through a form.

## Navigating the console

Each section of the hosted web console — Overview, Environments, Cloud contexts, and the rest of the sidebar — has its own URL. Reloading the page, sharing a link, or using the browser's Back and Forward buttons all keep you on the section you were viewing rather than dropping you back to Overview. A link naming a section your tenant type doesn't have (for example a non-operations tenant following a Users link) lands on Overview instead of a panel the API would refuse.

## Sign in once

```bash
erun cloud init erun --api-url https://api.erunpaas.com
erun cloud login --alias erun+api.erunpaas.com@erun
```

`https://api.erunpaas.com` is erun's own hosted platform — a single apex host serving every tenant, not a per-tenant or per-environment address. A self-hosted platform has its own single API URL the same way; ask whoever runs it what that is.

`login` opens a device-code sign-in (or a browser tab if your issuer has no device flow) and confirms you're in by printing your tenant.

In the [desktop app](/desktop/reviews#tenant-dashboard-connected), the same alias can be created without a terminal: the tenant dashboard's **Connect** action, or **Settings → Cloud aliases → Add erun platform**, both ask for just the API URL and sign in the same way.

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

In the desktop, the Registration tab's "Register an environment" form registers, and each row in its environments list carries its own Deploy control (with an optional version field) for a later re-deploy.

## Preview before you commit

`erun platform provision` resolves the full plan — quota, placement, namespace, and deploy — without creating anything, so you can check it before registering for real:

```bash
erun platform provision --env-name staging --env-type runtime
```

The Registration tab's "Register an environment" form previews the same way: a "Preview provisioning plan" button resolves and shows the plan before "Register environment" is even enabled to click, so a register action is never one click past a preview you have not seen.

## Stop and delete

```bash
erun platform env stop <environment-id>     # scale to zero; state survives
erun platform env delete <environment-id>   # start tearing down the namespace; irreversible
```

`delete` is irreversible, and it returns as soon as the platform has accepted it — the teardown itself runs in the background, because a namespace stuck on an unsatisfiable finalizer can sit in `Terminating` for a long time. The command prints the environment at status `deleting`; poll it the same way you polled the deploy:

```bash
erun platform env get <environment-id>
```

It converges one of two ways: the environment is gone (`env get` reports it as not found), or it lands on `deletion-blocked` with the reason — the stuck namespace's own conditions — printed on the same line. The platform re-attempts a blocked delete on its own every few minutes, so a namespace that finishes terminating converges without you doing anything; re-running `erun platform env delete` retries it immediately.

The Registration tab's environments list carries Stop and Delete alongside Deploy on each row. Delete additionally asks you to type the environment's name to confirm before it will send the request — the same confirmation every other unrecoverable action in the desktop app requires.

## Where an environment lands

Name a cloud context you've already registered with `contextId` and a hosted runtime environment deploys there instead of the platform's own cluster; leave it unset and the platform auto-selects one of your own registered contexts with room, or falls back to its own cluster if you haven't registered any. See [Placement](/concepts/hosted-platform#single-cluster-placement) for the full decision and what an unresolvable request looks like (a clear, immediate error rather than a silently-wrong deploy).

Register a cloud context first with:

```bash
erun platform context create --name prod --alias aws-main --region eu-west-2 --preview
erun platform context create --name prod --alias aws-main --region eu-west-2
```

`--preview` resolves and returns the bootstrap plan without creating anything, the same way the desktop's Registration tab's "Preview context plan" button does before its "Register context" button is used for real.

## Quotas

Your tenant has a cap on how many environments it may register at once. `erun platform env register` reports a clear conflict at the cap; `erun platform provision` shows you the same quota decision in its preview before you commit. An environment you have asked to delete stops counting against that cap as soon as the delete is accepted — a teardown that gets stuck can't lock you out of your own allowance. In the desktop, hitting the cap shows the same message inline on the register form rather than a raw error — it names the cap and the fix (delete or stop another environment first), the same recoverable state the CLI reports.

Each of your environments also runs inside a namespace capped on CPU, memory, and storage — enforced by Kubernetes itself, not just recorded. On top of that, your tenant has an aggregate CPU/memory/storage budget across all of your runtime environments combined: registering (or redeploying) one that would push your total past that budget is refused the same way, naming which resource and by how much. If your platform operator has set either cap unusually low, registering a new runtime environment is refused with a clear conflict naming the cap, rather than succeeding and failing to actually come up.

You can see your own tenant's full quota — the environment-count cap, the per-environment ceiling, and the aggregate budget — at any time via the API's `GET /v1/quota` (no operator role required); there is no CLI command for it yet. All caps are set by your platform operator (operations-only); reach out to them to raise any of them. See [Hosted platform · Quotas](/concepts/hosted-platform#quotas) for the full spec.

## Where next

- [`erun platform` CLI reference](/cli/platform) — every subcommand and flag.
- [Agent reference · Hosted platform](/concepts/hosted-platform) — the full lifecycle, placement, and RBAC spec.
- [Cloud contexts](/concepts/cloud-contexts) — the cluster model a future multi-cluster placement will build on.
