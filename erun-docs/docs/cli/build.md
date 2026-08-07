---
title: erun build
---

# `erun build`

Build the project's container image(s). `erun build` runs **only in agent envs** — runtime envs receive deploys of already-built artifacts. See [Environment types](/concepts/environment-types).

## Synopsis

```
erun build [flags]
```

## Where builds happen

In an agent env, `erun build` resolves the current Docker build context (from the current working directory), produces both `linux/amd64` and `linux/arm64` images via the local Docker daemon plus binfmt, and tags them so subsequent builds promote from cache instead of rebuilding. See [Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) for the cache-promotion algorithm.

`erun build` **mints the version** — that's its one extra job beyond building images. By default it mints a snapshot, the nearest `VERSION` file's base plus a timestamp (`<base>-snapshot-<UTC-timestamp>`); see [Build path resolution](/reference/configuration-build-paths) for the exact tag format and VERSION-walking rule. The version is a content identity, and `build` is the only command that creates one — [`push`](/cli/push) and [`deploy`](/cli/deploy) take it as input. To pin a bare version instead of a snapshot, pass `--release` (resolves and stamps the stable semver) or an explicit `--version` / a version carried by the build directory. The minted snapshot is the same whatever the env type — `build` never decides snapshot-vs-stable from the environment.

Runtime envs have no worktree and no source to build from — they receive already-built artifacts through [`erun deploy`](/cli/deploy). Running `erun build` where there is no Docker build context (such as a runtime env) fails because there is nothing to build, rather than producing an unexpected artifact.

If you run `erun build` in a project that has no `<tenant>-devops` build environment, it prints a one-line tip recommending the [`erun-build-env`](/agent-reference/skills-spec#erun-build-env) skill, which sets up that module — a `<tenant>-devops/docker/<tenant>-devops/Dockerfile` extending the published runtime image, plus the module's `VERSION` file — so you can customize the runtime image the environment runs.

Charts are build source too: `erun build` also packages every Helm chart under `<tenant>-devops/k8s/*` — or the [`paths.k8s`](/reference/configuration#paths-block) directory when configured — (running `helm dependency build` for umbrella charts, then `helm package`) at the minted version, to validate they build and record them in `--output json`. Packaging is local only — nothing is published until [`erun push`](/cli/push), which publishes the same charts to the registry.

`erun build` is `docker build` for each discovered component — it adds no separate test phase of its own. Run your tests in the Dockerfile's [builder stage](/agent-reference/conventions-spec#multi-stage-dockerfile-expectation): every test that doesn't depend on a deployed artefact (unit tests, and integration tests against in-build fixtures) belongs there, and a failure fails the build before any image is tagged. End-to-end tests that need a running deployment run after [`erun deploy`](/cli/deploy), not during build.

## Flags

| Flag | Description |
|---|---|
| `--deploy` | **Operator shortcut.** After a successful build, push the minted version and deploy it to the same environment — `build → push → deploy` in one command. |
| `--release` | **Operator shortcut.** Pin a stable release version instead of a snapshot and run the full [`erun release`](/cli/release) flow — publish the version, then tag it. |
| `--force` | Delete and recreate conflicting release tags when combined with `--release`. |
| `--dry-run` | Resolve and print every `docker build` / `docker tag` / `docker push` command without executing. |

`--deploy` and `--release` are **convenience shortcuts for an Operator at the terminal** — they compose the pure primitives so you don't have to type three commands. Programmatic callers (the desktop app, scripts, an Agent driving MCP) don't use them; they run `build`, `push`, and `deploy` themselves and thread the version between the steps. See [Command primitives](/concepts/command-primitives).

To capture the minted version for that kind of orchestration, run `erun build --output json`, which prints `{version, baseVersion, images}` on stdout — the version an orchestrator hands to `push` and `deploy`. `--output {text|json}` is a root flag available on every command (see [CLI flag spec · Common flags](/agent-reference/cli-flags)).

Advanced flags (`--no-incremental`, `--version`) and the full build lifecycle (binfmt verification, fingerprint resolution, per-arch build → manifest list, the `--output json` shape) are on [Agent reference · CLI flag spec · `erun build`](/agent-reference/cli-flags#erun-build).

## Examples

In an agent env:

```bash
erun build              # build the current Docker context, minting a fresh snapshot version
erun build --output json # same, and print {version, baseVersion, images} for an orchestrator
erun build --dry-run    # see exactly what would run
erun build --deploy     # operator shortcut: build → push → deploy in one shot
erun build --release    # operator shortcut: pin a stable version, then push + tag it
```

To get a built artifact into a runtime env, push the minted version and then deploy it: `erun push --version <version>` publishes the image and chart, and `erun deploy <env> --version <version>` rolls it out. See [`erun push`](/cli/push) and [`erun deploy`](/cli/deploy).

## Multi-architecture

Every build produces both `linux/amd64` and `linux/arm64`. There is no single-platform code path — a single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at build time on your machine, not at remote deploy time.

The local Docker daemon must have binfmt installed for the foreign arch. The runtime chart's `binfmt` init container installs this automatically inside the cluster; for local builds you may need to run `docker run --privileged --rm tonistiigi/binfmt --install all` once on your host.

An image whose Dockerfile builds `FROM` another image the same build produces resolves that base from the local build, for each architecture, without the base being published. So `erun build --version <version>` builds a whole release locally — dependent images included — which makes it usable as the gate to run *before* [`erun release`](/cli/release) moves any git ref. Nothing local is tagged as the plain published version; assembling that multi-arch manifest stays [`erun push`](/cli/push)'s job. See [Agent reference · CLI flag spec · `erun build`](/agent-reference/cli-flags#erun-build) for the exact build-arg rule.

## `--dry-run` output

`erun build --dry-run` streams the same `audit:` and `trace:` lines a real run would: the resolved build scope (project root, tenant, environment, version, registry), the per-component fingerprint-cache decision, and the `docker build` (one per architecture), `docker tag`, and — with `--release` — `docker push` / manifest commands it would run, without executing any of them. Values matching secret patterns are redacted. The trace is otherwise identical to the real run. Redaction follows the rules in [Agent reference · Dry-run redaction](/agent-reference/dry-run-redaction).

## Error behaviour

| Failure | Behaviour |
|---|---|
| No Docker build context in scope (e.g. a runtime env with no worktree). | Errors that no build context was found; nothing is built. |
| Foreign-arch binfmt missing locally. | Fails with a direct error before the per-arch build, rather than a confusing mid-build failure. |
| A dependent image's base is not published at the version. | Not a failure: a base this build produces is resolved from the local build, per architecture. Only a base that no build in scope produces has to exist in the registry. |
| Registry rejects a `--release` push as unauthorised. | Retries with `docker login` (requires a TTY); see [`erun push`](/cli/push) authentication. |
| `--version` combined with `--release`. | Rejected — `--release` resolves the version itself. |
