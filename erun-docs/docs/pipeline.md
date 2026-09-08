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

The four steps are **pure primitives** — each does exactly one thing, with no environment-type or env-name decision logic inside it. The unit that flows between them is the **version**: a content identity that [`build`](/cli/build) mints and the later steps consume. See [Command primitives](/concepts/command-primitives) for the full model.

- **[`build`](/cli/build)** — compile the project's container images, multi-arch (`linux/amd64` + `linux/arm64`) by default and fingerprint-cached, and **mint the version** (a snapshot by default; `--release` pins a bare semver). `build` is the only step that creates a version. A `--release` build always targets both architectures; a non-release build/push may be narrowed to one for an environment whose cluster can only ever run it (see [Multi-architecture](/cli/build#multi-architecture)).
- **[`release`](/cli/release)** — orchestrate build → push → git-tag for a stable, immutable version: bump the semver, update the version-bearing files, build, push, commit, and tag. It reuses `push` for all publishing. See [Versioning](/versioning).
- **[`push`](/cli/push)** — publish a version's outputs to the project's container registry: the multi-arch image **and** the runtime helm chart, together at the same version.
- **[`deploy`](/cli/deploy)** — install a published version into an environment with a Helm upgrade, by reference. It never builds or pushes; a version is required.

You rarely run the four by hand. For an Operator at the terminal, **convenience shortcuts** compose them: `erun build --release` folds the release flow into the build, `erun build --deploy` carries one build straight through push and rollout, and `erun push --build` builds the current source then publishes the version it mints — so one command runs the flow and the version threads through for you. Programmatic callers (the desktop app, scripts, an Agent over MCP) don't use the shortcuts; they run the primitives themselves and thread the version (captured from `erun build --output json`), keeping the "for this env type, do build→push→deploy" policy in the caller, not the command.

## What `build` does

`build` turns the source in an [agent env](/concepts/environment-types) into versioned container images — it runs **only in an agent env**, since a [runtime env](/concepts/environment-types) has no source and only receives already-built artefacts through `deploy`. It resolves the whole build from the project's [conventions](/concepts/conventions); there's nothing per-project to wire up.

### How it finds what to build

It discovers each component's Dockerfile under the tenant's devops module — every `docker/<component>/` directory is one image, named with the tenant prefix (`petios-api`, not bare `api`) — and tags each with the version from the nearest `VERSION` file.

<figure className="erun-hero-figure">
  <img src="/img/build-discovery.svg" alt="Under the project root, the petios-devops/docker/ directory holds one tenant-prefixed directory per component — petios-api, petios-web, petios-worker — each containing a Dockerfile and an optional VERSION file. Every such directory is one image." />
</figure>

Build order follows the `FROM …:${ERUN_VERSION}` links between components; component-naming rules and the rest are in [Build path resolution](/reference/configuration-build-paths) and the [conventions spec](/agent-reference/conventions-spec#component-naming).

### The steps

Each component builds for both architectures, reuses unchanged layers from the [fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache), and is tagged with the resolved version.

<figure className="erun-hero-figure">
  <img src="/img/build-steps.svg" alt="Per component, left to right: from the component plus its Dockerfile, build for amd64 and arm64 (reusing unchanged layers from the cache), then tag with the version (snapshot or release), producing the container image." />
</figure>

A snapshot version while you iterate; `--release` pins a stable bare version instead. Either way, the multi-arch manifest list is assembled when the version is published by [`push`](/cli/push). Full contract: [multi-architecture build](/agent-reference/conventions-spec#multi-architecture-build-contract).

### How you set it up — and where tests run

Each component's Dockerfile is a [multi-stage build](/agent-reference/conventions-spec#multi-stage-dockerfile-expectation): a builder stage that compiles the artefact and runs the tests, then a thin runtime stage that ships only the artefact.

<figure className="erun-hero-figure">
  <img src="/img/build-stages.svg" alt="One Dockerfile with two stages: a builder stage that compiles the artefact and runs the tests, and a runtime stage that ships only that artefact, producing a tagged container image for amd64 and arm64." />
</figure>

Run every test that doesn't need a deployed artefact (unit tests, and integration tests against in-build fixtures) in the builder stage — because `build` is `docker build`, a failing test fails the build and no image is tagged, so a green build is a tested build that marks a [review](/collaboration/reviews) `READY`. End-to-end tests that need a running deployment run after `deploy`, via [`erun e2e`](#what-e2e-does) below.

See [`erun build`](/cli/build) for flags, dry-run output, and error behaviour.

## What `e2e` does {#what-e2e-does}

`build`/`push`/`deploy` prove an image builds and installs; they cannot prove the thing it installed actually serves — a stale rollout can look identical to a fresh one from the outside. [`erun e2e`](/cli/e2e) is the step that closes that gap: it discovers a `playwright/` folder the same way `build` discovers `docker/`, then runs it once against an already-deployed environment with two values injected that the suite never declares itself — the resolved HTTPS base URL and the version the environment is actually running. The suite's first assertion is that the served surface reports that version, so a green run against a stale deployment is no longer indistinguishable from a real pass.

`erun e2e` refuses before a browser starts, naming the cause, when the environment isn't deployed, the target service isn't exposed, or its certificate isn't issued yet — the three conditions that otherwise surface as an opaque connection timeout or TLS error deep inside a Playwright run. It also refuses a suite that sets `ignoreHTTPSErrors` or hardcodes its own `baseURL`, since both would silently defeat the guarantee above. A project with no `playwright/` folder makes it a clean no-op.

It is a separate step with its own exit code — `erun deploy` never runs it as a side effect. `erun build --e2e` is the everyday shortcut for the branch flow: it composes build → push → deploy → e2e, the same way `build --deploy` already composes build → push → deploy.

See [`erun e2e`](/cli/e2e) and [Conventions spec · Playwright suite discovery](/agent-reference/conventions-spec#playwright-suite-discovery).

## Two ways to ship

`release` is for stable, promotable versions — but you don't always need one, and that's the pipeline's range:

- **Snapshot — iterate.** Skip `release`: `build` mints a snapshot version, `push --version <snapshot>` publishes it (image + chart), and `deploy <env> --version <snapshot>` rolls it out. Because push publishes the chart for snapshots too, you can deploy a snapshot to a **target environment** — a shared or remote env, not just your local one.
- **Release — promote.** Run `release` to cut a tagged, immutable version (build → push → tag), then `deploy <env> --version <release>` promotes it to a [runtime env](/concepts/environment-types).

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
- **CI** — before a review can be accepted, ERun's own [merge queue](/collaboration/merge-queue) builds the prospective merge of its source onto its *current* target and gates it with a real `erun build`, pushing only on green — the step that catches two reviews that are each green alone but broken together, before the target branch moves. Once a review actually merges this way, it triggers `erun release` through the [release queue](/collaboration/builds#release-queue), which runs it in an agent env with warm caches, one release at a time per tenant; a later `erun deploy --version` rolls the published version out.

## Promotion: agent env to runtime env

The fullest shape of the pipeline is promoting a change from where you build it to where it serves. You develop and `build` in an [agent env](/concepts/environment-types), iterate by deploying snapshots, then cut a stable version with `release` and `deploy` it into a [runtime env](/concepts/environment-types) — which only ever receives already-built artifacts, it never builds. [Versioning](/versioning) covers the snapshot-versus-release mechanics; [environment types](/concepts/environment-types) covers why the two kinds of env exist.

## Where next

- **[Versioning](/versioning)** — how snapshot and release versions are generated.
- **[`erun e2e`](/cli/e2e)** — post-deploy verification against a real, deployed environment.
- **[CLI overview](/cli/overview)** — every command, grouped by what it's for.
- **[Build a small app](/getting-started/build-an-app)** — the pipeline end to end, from a blank directory.
- **[Environment types](/concepts/environment-types)** — agent envs build; runtime envs receive.
