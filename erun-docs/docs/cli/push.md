---
title: erun push
---

# `erun push`

Push built container images to the configured container registry.

## Synopsis

```
erun push [flags]
```

## Behavior by environment type

### Local environment

`erun push` resolves the current build context, **rebuilds** the image with a fresh snapshot tag (`<semver>-snapshot-<UTC-timestamp>`), then pushes per-arch tags and assembles a multi-arch manifest list. This matches `erun build --release`'s push path — `push` for local is build+push.

### Non-local environment

`erun push` **skips the build step** and runs `docker push` directly against the tag `<registry>/<image>:<VERSION>`. It assumes the image already exists in the local docker daemon (typically produced by a prior `erun build`, which delegates to your project's `build.sh`).

This split exists because non-local environments use stable release tags from the `VERSION` file. Silently rebuilding and overwriting those tags would mutate release artifacts — `push` is the explicit "promote what was built" step.

## Flags

| Flag | Description |
|---|---|
| `--force` | Rebuild and re-push every image, bypassing the fingerprint cache. Only meaningful for the local environment. |
| `--dry-run` | Resolve and print every `docker push` command without executing. |

## Registry resolution

The registry used in the push tag comes from (in priority order):

1. The environment's `EnvConfig.ContainerRegistry`.
2. The project's `.erun/config.yaml` `environments.<env>.containerregistry`.
3. The project's top-level `containerregistry` in `.erun/config.yaml`.
4. The default `ghcr.io/sophium`.

See [Container registries](/deployment/registries) for more detail.

## Examples

Push from a local environment (rebuilds + pushes):

```bash
erun push --dry-run
erun push
```

Push from a non-local environment (after `erun build` has run):

```bash
erun build       # runs ./build.sh — produces tagged images
erun push        # docker push the tagged images
```

## Authentication

If the registry rejects the push with `unauthorized`/`denied`/`insufficient_scope`, `erun push` retries with an interactive `docker login` prompt. For GHCR specifically, when the failure looks like a token-scope mismatch (`does not match expected scopes`, `permission_denied`), `erun push` attempts a namespace-owner re-login via `gh auth` automatically.
