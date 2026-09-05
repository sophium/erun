---
title: FAQ
---

# FAQ

## Is ERun an AI-only tool? Can I use it without an Agent?

Yes — the desktop app and CLI are equally useful as a clean development surface for human-driven work. Open as many isolated environments as your machine can host, attach an IDE, do your thing. The MCP endpoint exists but no Agent has to connect. See [Desktop · Works with or without an Agent](/desktop/working-with-an-agent#works-with-or-without-an-agent).

## Does ERun replace Kubernetes? Or CI/CD? Or my IDE?

No. ERun sits *alongside* them. Kubernetes is the isolation primitive ERun manages; your IDE attaches over SSH; your CI is what runs `erun build --release` on main. ERun's job is removing the daily friction in front of those tools — not replacing any of them.

## Can I use my existing CI pipeline (GitHub Actions, GitLab, Buildkite, …)?

Yes. The CI workflow is just `erun build --release` (on main) and `erun deploy` (to runtime envs). Anything that can run a binary can do that. ERun also exposes its own [build records API](/collaboration/builds) so the CI can publish build outcomes into the review/merge-queue model.

## What about Windows?

Install via Scoop. Docker Desktop with Kubernetes enabled is the standard local setup; OrbStack and Rancher Desktop work too. The desktop app runs natively on macOS and Windows.

## How do I share an environment with a teammate?

Two options:

- **Same machine** — impossible by design; envs are bound to one operator's local config. For shared dev, use a cloud context.
- **Cloud context** — both teammates' tenants reference the same `cloudcontexts[]` entry; they open the env from their own machines. The runtime pod is in the cloud, both operators see the same workspace.

The collaboration model (reviews, comments, builds, merge queue) is designed for many operators across many envs — see [Operator + Agent overview](/collaboration/overview).

## Can an Agent access my AWS or GCP credentials?

Only when you attach an AWS cloud alias to that env. An alias is a credential you already authenticated, so associating it with an env is you authorizing the env to act on your behalf — ERun then keeps the runtime pod's `~/.aws` seeded from that alias's host profile. An env with no AWS alias attached has only the cluster's ServiceAccount permissions — the same RBAC scope an operator's shell would have in that pod.

## How much does ERun cost?

The software is open source (Apache 2.0). What costs money is:

- Your laptop's compute, if you run envs locally.
- Your cloud provider's compute, when an env runs on a cloud context. ERun shuts the cloud context down when it goes idle; you pay only for the time it's running.

ERun itself doesn't add a service fee — there's no SaaS subscription in the loop.

## What if my project doesn't follow ERun's conventions?

Most things degrade gracefully. For your own components, ERun expects multi-stage Dockerfiles under `<module>/docker/<image>/` and helm charts under `<module>/k8s/<component>/`. Without those, you can either:

- Use `<command>.sh` overrides to plug in your own build/deploy logic — see [Conventions · Command overrides](/concepts/conventions#command-overrides-via-commandsh).
- Adopt the conventional layout by hand (or ask the Agent to — the built-in skills know the shape).

The env's own runtime needs nothing in your repo: it deploys from the published `erun-devops` chart and image.

Single-stage Dockerfiles, flat directory layouts, custom command logic — all work, you just get less out of ERun's defaults.

## Can ERun manage multiple projects (tenants) on the same machine?

Yes. Each tenant has its own folder under `~/.config/erun/<tenant>/` and each env has its own Kubernetes namespace. There's no shared state between tenants — open as many as your machine can host. Per-operator + per-tenant env naming patterns (`<operator>-develop`, `<operator>-hotfix`, …) are the convention for keeping things organised.

## How do I deal with secrets?

ERun uses Kubernetes' native `Secret` primitive. Your helm charts reference them via `envFrom: secretRef:` or volume mounts. See [Inside an environment · Secrets](/concepts/runtime-pods#secrets) for the full breakdown.

`--dry-run` redacts secret-like values automatically — see [Agent reference · Dry-run redaction](/agent-reference/dry-run-redaction).

## Where does state live? What gets backed up?

- **Per-user config** — `~/.config/erun/` on your machine. Back it up with the rest of your home directory.
- **Per-project config** — `<repo>/.erun/config.yaml`, committed to git.
- **In-env state** — Kubernetes PVCs in the env's namespace. Treat them as ephemeral by default: the namespace can be dropped and recreated. For data you want durable, run a stateful service (Postgres, etc.) in the env and configure its standard backup/restore.
- **API state** — reviews, comments, builds, merge queue — lives in the hosted erun API's database. Persistence is administered there.

## How do I report a bug or request a feature?

ERun is on GitHub: [github.com/sophium/erun](https://github.com/sophium/erun). Bugs go in issues; feature requests in discussions; security issues to the contact email in the security policy.
