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

## Checking an environment's MCP edge from the console

The console's **MCP access** panel mints a per-environment MCP bearer token (`POST /v1/environments/{id}/mcp-token` — see [Agent reference · API protocol](/agent-reference/api-protocol#mcp-token-endpoint)) for you to present to the environment's MCP edge with your own MCP client. A **Token capability** selector controls what that token can do: **Operate** (the default — deploy an already-published version, start/stop the cloud context, resize the runtime pod) or **Admin** (every tool, including `raw`, `delete`, and `terraform`). Operate needs no special entitlement; requesting Admin does — it takes the same permission as deleting the environment outright, so it is an explicit escalation rather than the default.

It needs the environment's MCP hostname, which the panel does not resolve for you:

- A `runtime` environment already has one — the platform wires exposure into the same deploy (see [Automatic exposure](/concepts/hosted-platform#automatic-exposure) above), at `mcp.<tenant>-<env>.<services-zone>`.
- Any other environment type needs it exposed first: `erun expose <tenant> <env> mcp` prints the hostname to paste into the panel.

With an **Admin**-scoped token, paste that hostname in and click **Call the version tool** as a connectivity check: a JSON result confirms the edge is reachable and the token is accepted, and a network error most often means the hostname isn't exposed yet or the platform has no MCP signing key configured (the panel's mint step already reports that case with a 501). This is a connectivity check, not a general MCP client: it only ever calls the one read-only tool — and it needs Admin because reading (`version`) is not part of what Operate grants.

An **Operate**-scoped token skips that connectivity check (calling `version` would always be refused) and instead shows a **Drive an operate tool** form: pick `deploy`, `context_start`, `context_stop`, or `resize`, fill in that tool's own inputs (a version to deploy, the cloud context's name, a runtime-pod size), and call it against the same hostname — no external MCP client needed. **Preview** is checked by default on every call, so the first attempt against a real environment resolves and traces the plan without changing anything; uncheck it once the plan looks right.

The same panel's **Attach to a live session** control opens a live terminal to a session already running in the environment's pod (the one `erun open --ai` or a linked desktop orchestrator started), directly in the browser — no port-forward, no separate SSH or attach client. It mints its own narrower token (`erun:attach`, not the `erun:admin` the tool-driving form above uses). The session id field discovers what the environment has actually reported: it prefills the most recently active live session and offers a quick-pick button for every other one, so you no longer need to already know the id from wherever the session was started; you can still type one by hand, including for an environment that has not reported any sessions yet. Type a line and press **Send**; output streams into the scrollback as it arrives. This is a minimal, line-based view rather than a full terminal (no cursor-addressed rendering yet), and **Disconnect** ends only this browser's view — the session itself keeps running for the next attach.

## Keep an environment's settings on the platform

Registering an environment does **not** upload how it is configured. The platform row holds identity and lifecycle; the settings that describe the environment itself — its runtime version and image, its sizing, its idle policy, which AI tool its agent uses — live in your local config until you upload them.

```bash
erun platform env register --name dev --type local-agent --adopt \
  --kubernetes-context kind-dev --definition acme/dev
```

`--adopt` records a row for an environment that already exists on your machine instead of asking the platform to provision one. Paired with `--definition TENANT/ENVIRONMENT`, it also uploads that environment's portable settings as the row's **definition revision 1** and writes a **hosted marker** into the local config, recording which platform row this machine follows.

### The hosted marker

The marker is a local fact, not server state: on the platform every row is hosted by definition, so "this environment is hosted" can only mean *this local environment corresponds to that platform row*. It records four things:

| Element | What it names |
|---|---|
| API host | which platform — `api.erunpaas.com`, or a self-hosted one |
| Tenant id | the tenant the platform resolved from your token's issuer, not the directory name |
| Environment id | the row itself |
| Definition revision | the revision this machine last synced from |

All four are needed. Two platforms can hold rows that share an environment id in the sense that they are unrelated, and a tenant directory name is not a platform tenant — so no single element identifies the row. `erun platform env push` and `pull` compare the whole marker against what the platform resolves and **refuse** on a disagreement rather than re-pointing the environment: adopting the resolved identity would silently write your settings to a different tenant's row.

The marker is visible in `erun list`, on the `hosted:` line beside `managed-cloud:`. The two are different facts — `managed-cloud` says the platform manages this environment's *lifecycle*, `hosted` says it holds this environment's *definition* — so an environment can be either, both, or neither.

### Upload and pull

```bash
erun platform env push acme/dev     # upload the current settings; advances the revision
erun platform env pull acme/dev      # bring the platform's copy back down
```

`push` uploads; `pull` reads the row and its stored definition and writes the portable subset into `erun/<tenant>/<env>/config.yaml`. Both accept `--dry-run`, which resolves and traces what would travel without contacting the platform or writing anything.

A `pull` into an environment this machine does not have yet needs `--environment-id` — nothing local can say which row you mean:

```bash
erun platform env pull acme/dev --environment-id 018f4b2a-... --repo-path ~/src/acme
```

That path is checked *before* anything is written. The repository path is a host-owned setting, so a pull never obtains one from the platform; for the types that require it (`local-agent`), a pull-as-new without a usable `--repo-path` is refused rather than creating an environment that cannot build. The same applies to the local port range: a newcomer gets the lowest free range on this machine, and a collision with an environment that already holds one is refused before the write rather than discovered later by whatever needs a port.

### Which settings never leave the machine

The rule is a positive allowlist: a setting travels only if it describes the environment itself. Everything else is host-owned — it describes *this machine*, or names an object that only exists on *this* cluster — and is never uploaded and never written by a pull.

**Travels:** the runtime version, runtime image and runtime chart; the runtime and dind pod sizing; the namespace quota; the idle policy; the agent configuration; which AI tool the environment uses; whether it rides upgrades and on which channel; the marked container registry list; and `deploy.timeout`.

**Never travels:** the repo path and the local port range; sshd keys, ports and paths; the MCP auth public key path; `imagePullSecrets`, `registryCredentialSecretName` and `platformAliasSecretName` (each names a Kubernetes Secret, and a name is a cluster-local reference whose absence elsewhere is not an error); cloud provider aliases; `managedCloud`; the runtime registry endpoint; host credential delivery; `platformAccount`; auto-start and the stopped flag; whether the build script is disabled; `deploy.components`, which is this machine's own saved selection; and the hosted marker itself.

**Refuses instead of merging:** three settings mirror columns on the row and are fixed at registration — the environment's **name**, its **type**, and its **Kubernetes context**. A local value that disagrees is a refusal, not a merge, and the name is the load-bearing one: the local config path is derived from it, so pulling a differently-named row would write a second environment instead of updating this one.

The root `config.yaml` and the secret store are permanently out of scope. The root file holds the plaintext admin token, and a transfer that could reach it would be a credential-exfiltration path rather than a convenience.

### When both sides have changed

Only portable settings can genuinely conflict. A pull compares the revision this machine last synced from against the revision the platform holds; when the platform has moved ahead, that is a catch-up and the pull applies it. When *this machine* has also moved past the revision it last synced from, the same setting may have been edited on both sides, and the pull shows you the diff and asks:

```
  This environment and the platform have both changed since the last sync:
    runtime version: 1.2.2 -> 1.2.3
```

Answer `y` to take the platform's copy, or `n` to leave your own in place. `-y` accepts without asking, for non-interactive callers; with no way to ask at all (an MCP caller), a two-sided edit is a refusal carrying the diff rather than a silent overwrite.

A pull preserves everything the platform has no opinion about, including keys this erun does not model.

### Knowing when the platform has moved

`erun list` shows the revision your local copy was synced from. In the desktop app, the environment's **General** tab carries a **Hosted environment** panel: which row this environment corresponds to, and a **Check for updates** button that compares revisions and tells you when the platform is ahead. The panel is read-only by design — it names `erun platform env pull` rather than performing it, so a config change never triggers a write of its own.

## Quotas

Your tenant has a cap on how many environments it may register at once. `erun platform env register` reports a clear conflict at the cap; `erun platform provision` shows you the same quota decision in its preview before you commit. An environment you have asked to delete stops counting against that cap as soon as the delete is accepted — a teardown that gets stuck can't lock you out of your own allowance. In the desktop, hitting the cap shows the same message inline on the register form rather than a raw error — it names the cap and the fix (delete or stop another environment first), the same recoverable state the CLI reports.

Each of your environments also runs inside a namespace capped on CPU, memory, and storage — enforced by Kubernetes itself, not just recorded. On top of that, your tenant has an aggregate CPU/memory/storage budget across all of your runtime environments combined: registering (or redeploying) one that would push your total past that budget is refused the same way, naming which resource and by how much. If your platform operator has set either cap unusually low, registering a new runtime environment is refused with a clear conflict naming the cap, rather than succeeding and failing to actually come up.

You can see your own tenant's full quota — the environment-count cap, the per-environment ceiling, and the aggregate budget — at any time via the API's `GET /v1/quota` (no operator role required); there is no CLI command for it yet. All caps are set by your platform operator (operations-only); reach out to them to raise any of them. See [Hosted platform · Quotas](/concepts/hosted-platform#quotas) for the full spec.

## Where next

- [`erun platform` CLI reference](/cli/platform) — every subcommand and flag.
- [Agent reference · Hosted platform](/concepts/hosted-platform) — the full lifecycle, placement, and RBAC spec.
- [Cloud contexts](/concepts/cloud-contexts) — the cluster model a future multi-cluster placement will build on.
- [Administering another tenant](/collaboration/cross-tenant-administration) — an OPERATIONS Operator viewing and registering environments in a different tenant.
