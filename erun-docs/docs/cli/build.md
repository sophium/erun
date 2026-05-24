---
title: erun build
---

# `erun build`

Build the project's container image(s). Behavior depends on whether the active environment is `local`.

## Synopsis

```
erun build [flags]
```

## Behavior by environment type

### Local environment (env named `local`)

`erun build` resolves the current Docker build context (from the current working directory), produces both `linux/amd64` and `linux/arm64` images via the local Docker daemon plus binfmt, and applies content-derived fingerprint tags (`<image>:fp-<hash>-<arch>`) so subsequent builds promote from cache instead of rebuilding.

The image tag is a **snapshot**: `<semver>-snapshot-<UTC-timestamp>`. The semver comes from the nearest `VERSION` file walking up from the build directory.

### Non-local environment (any other name)

`erun build` looks for `<projectRoot>/build.sh` (or a nested `*/build.sh` under the project root) and runs it. ERun does not produce images directly in this mode — the project's script does, with whatever tags it chooses.

The expectation is that `build.sh` tags images as `<registry>/<image>:<VERSION>` so that the subsequent `erun push` finds them in the local docker daemon.

## Flags

| Flag | Description |
|---|---|
| `--deploy` | After a successful build, run `erun deploy` for the same environment. |
| `--release` | Run `erun release` first and publish the release-tagged images. |
| `--force` | Delete and recreate conflicting release tags when combined with `--release`. |
| `--no-incremental` | Disable fingerprint-based build caching and rebuild every image from scratch. |
| `--version <version>` | Override the resolved image version. |
| `--dry-run` | Resolve and print every `docker build` / `docker tag` / `docker push` command without executing. |

## Examples

Local iteration:

```bash
erun build              # rebuild current Docker context with a fresh snapshot tag
erun build --dry-run    # see exactly what would run
erun build --deploy     # build then deploy in one shot
```

Non-local (from inside a runtime pod):

```bash
erun build                       # runs ./build.sh in the project root
erun build --dry-run             # shows the build.sh invocation
```

## Multi-architecture

Every build produces both `linux/amd64` and `linux/arm64`. There is no single-platform code path — a single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at developer-machine build time, not at remote deploy time.

The local Docker daemon must have binfmt installed for the foreign arch. The runtime chart's `binfmt` init container installs this automatically inside the cluster; for local builds you may need to run `docker run --privileged --rm tonistiigi/binfmt --install all` once on your host.
