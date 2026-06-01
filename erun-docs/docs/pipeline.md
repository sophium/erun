---
title: Delivery pipeline
slug: /pipeline
---

# Delivery pipeline

ERun's other half. The [environment model](/intro) gives you a namespace per task; the delivery pipeline gives every one of those environments the same way to ship code: **`build → release → push → deploy`**. Whatever the stack — a LAMP app on a VM, a Go service, an autoscaling enterprise system — it ships the same way.

<figure className="erun-hero-figure">
  <img src="/img/pipeline.svg" alt="The delivery pipeline: four cyan-outlined steps left to right — build (images, multi-arch), release (stable version, tag), push (to registry), deploy (helm upgrade) — with a dashed bypass arc from build over release to push labelled 'snapshot skips release'. From deploy, two arrows reach charcoal environment pills: a target env (snapshot, iterate) and a runtime env (release, promote). A band reads: drive it from the CLI, the desktop app, MCP, or CI — any stack, the same flow." />
  <figcaption>build → release → push → deploy. A snapshot skips release to land in a target env to iterate; a release is promoted to a runtime env.</figcaption>
</figure>

## The steps

- **[`build`](/cli/build)** — compile the project's container images, always multi-arch (`linux/amd64` + `linux/arm64`) and fingerprint-cached. Each image is tagged with a version.
- **[`release`](/cli/release)** — stamp a stable, immutable version: bump the semver, update the version-bearing files, commit, tag it, and push the tag. See [Versioning](/versioning).
- **[`push`](/cli/push)** — publish the built images to the project's container registry.
- **[`deploy`](/cli/deploy)** — roll a version out to an environment with a Helm upgrade, building and pushing whatever it needs first.

You rarely run the steps by hand. `erun build --release` folds the release step into the build, and `erun build --deploy` carries straight through push and rollout — so one command runs the flow and the version threads through for you.

## Two ways to ship

`release` is for stable, promotable versions — but you don't always need one, and that's the pipeline's range:

- **Snapshot — iterate.** Skip `release`: a snapshot build goes straight `build → push → deploy` into an environment. You can deploy a snapshot to a **target environment** — `erun deploy <tenant> <environment>` puts it there — so you iterate against a shared or remote env, not just your local one.
- **Release — promote.** Run `release` to cut a tagged, immutable version, then `deploy` promotes it to a [runtime env](/concepts/environment-types).

Same pipeline; the only difference is whether you stop to cut a release.

## One flow, any stack

ERun doesn't ship a build system. It supplies the [conventions](/concepts/conventions) — where a component's Dockerfile lives, how its Helm chart is named, how versions are tagged — so the steps resolve the same way no matter what's in the repo. A new service added to a project inherits the pipeline automatically; there's nothing per-project to wire up. That's why the same `erun deploy` works for a single container and for a system with a database, a queue, and a fleet of services.

## One flow, any driver

The pipeline is the same whether a person or a machine runs it.

<figure className="erun-hero-figure">
  <img src="/img/pipeline-drivers.svg" alt="Four charcoal pills on the left — CLI, desktop app, MCP, CI — each with an arrow converging into a single cyan box labelled 'delivery pipeline' with the subtitle build · release · push · deploy. One arrow leaves the box to a charcoal pill labelled 'your environments'." />
  <figcaption>Four ways in, one pipeline underneath — so a terminal preview and an Agent's MCP deploy take the same steps.</figcaption>
</figure>

- **CLI** — `erun build` / `release` / `push` / `deploy`, scriptable and headless. See the [CLI overview](/cli/overview).
- **Desktop app** — the same commands behind buttons. See the [desktop app](/desktop/overview).
- **MCP** — an Agent calls the `build` / `push` / `deploy` / `release` [tools](/mcp/overview), which run the identical logic and return structured results instead of stdout.
- **CI** — a release-tagged commit triggers `erun release`; a later `erun deploy` rolls the published version out.

## Promotion: agent env to runtime env

The fullest shape of the pipeline is promoting a change from where you build it to where it serves. You develop and `build` in an [agent env](/concepts/environment-types), iterate by deploying snapshots, then cut a stable version with `release` and `deploy` it into a [runtime env](/concepts/environment-types) — which only ever receives already-built artifacts, it never builds. [Versioning](/versioning) covers the snapshot-versus-release mechanics; [environment types](/concepts/environment-types) covers why the two kinds of env exist.

## Where next

- **[Versioning](/versioning)** — how snapshot and release versions are generated.
- **[CLI overview](/cli/overview)** — every command, grouped by what it's for.
- **[Build a small app](/getting-started/build-an-app)** — the pipeline end to end, from a blank directory.
- **[Environment types](/concepts/environment-types)** — agent envs build; runtime envs receive.
