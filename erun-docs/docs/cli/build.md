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

The image tag in an agent env is a snapshot built from the nearest `VERSION` file plus a timestamp; see [Build path resolution](/reference/configuration-build-paths) for the exact tag format and VERSION-walking rule. To produce a stable release tag (for promotion to a runtime env), use `erun build --release`.

Runtime envs have no worktree and no source to build from — they receive already-built artifacts through [`erun deploy`](/cli/deploy). Running `erun build` where there is no Docker build context (such as a runtime env) fails because there is nothing to build, rather than producing an unexpected artifact.

**(Planned — [#471](https://github.com/sophium/erun/issues/471).)** Before it produces the images, `erun build` runs the project's unit and integration tests and aborts the build if any fail — so a successful build is always a *tested* artifact. Test execution surfaces in the `--dry-run` trace like every other build action. Until this lands, `erun build` only compiles images; run your test suite as a separate step.

## Flags

| Flag | Description |
|---|---|
| `--deploy` | After a successful build, run `erun deploy` for the same environment. |
| `--release` | Run `erun release` first and publish the release-tagged images. |
| `--force` | Delete and recreate conflicting release tags when combined with `--release`. |
| `--dry-run` | Resolve and print every `docker build` / `docker tag` / `docker push` command without executing. |

Advanced flags (`--no-incremental`, `--version`) and the full build lifecycle (binfmt verification, fingerprint resolution, per-arch build → manifest list) are on [Agent reference · CLI flag spec · `erun build`](/agent-reference/cli-flags#erun-build).

## Examples

In an agent env:

```bash
erun build              # rebuild current Docker context with a fresh snapshot tag
erun build --dry-run    # see exactly what would run
erun build --deploy     # build then deploy in one shot
erun build --release    # produce a stable release tag for promotion to a runtime env
```

To get a built artifact into a runtime env, run `erun deploy` against that env — it picks up the already-built version from the registry. See [`erun deploy`](/cli/deploy).

## Multi-architecture

Every build produces both `linux/amd64` and `linux/arm64`. There is no single-platform code path — a single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at build time on your machine, not at remote deploy time.

The local Docker daemon must have binfmt installed for the foreign arch. The runtime chart's `binfmt` init container installs this automatically inside the cluster; for local builds you may need to run `docker run --privileged --rm tonistiigi/binfmt --install all` once on your host.

## `--dry-run` output

`erun build --dry-run` streams the same `audit:` and `trace:` lines a real run would: the resolved build scope (project root, tenant, environment, version, registry), the per-component fingerprint-cache decision, and the `docker build` (one per architecture), `docker tag`, and — with `--release` — `docker push` / manifest commands it would run, without executing any of them. Values matching secret patterns are redacted. The trace is otherwise identical to the real run. Redaction follows the rules in [Agent reference · Dry-run redaction](/agent-reference/dry-run-redaction).

## Error behaviour

| Failure | Behaviour |
|---|---|
| No Docker build context in scope (e.g. a runtime env with no worktree). | Errors that no build context was found; nothing is built. |
| Foreign-arch binfmt missing locally. | Fails with a direct error before the per-arch build, rather than a confusing mid-build failure. |
| Registry rejects a `--release` push as unauthorised. | Retries with `docker login` (requires a TTY); see [`erun push`](/cli/push) authentication. |
| `--version` combined with `--release`. | Rejected — `--release` resolves the version itself. |
