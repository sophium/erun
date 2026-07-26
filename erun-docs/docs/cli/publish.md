---
title: erun publish
---

# `erun publish`

Mirror an already-built version's images from the `from` registry to each `to` registry, without building or deploying. `erun publish <tenant> <env> --version <version>` is a pure primitive: use it to hand a version you have iterated on and tested — for example one built against a [local cluster registry](/deployment/registries#cluster-registries-resolved-from-the-context) — to other users, by copying that exact multi-arch image to a shared registry such as `ghcr.io/<org>`.

## Synopsis

```
erun publish [TENANT] [ENVIRONMENT] --version <version> [flags]
```

The version is **required** — it is a content identity minted by [`erun build`](/cli/build); `publish` never builds one. The environment's [registry list](/deployment/registries) must mark a `from` source and at least one `to` destination, or there is nothing to mirror.

## What publish does

For the given version, `erun publish` resolves the same image set a [deploy](/cli/deploy) would (the runtime image and any chart-referenced component images), then copies each from the `from` registry to every `to` registry with `docker buildx imagetools create` — a manifest-aware, registry-to-registry copy that preserves the `linux/amd64` + `linux/arm64` manifest list. It **never** rebuilds, redeploys, or touches git.

This is the deliberate counterpart to the deploy-time mirror: where [`erun deploy`](/cli/deploy) copies `from`→`to` as a side effect of every rollout, `publish` lets you iterate freely against your build/deploy registry and share the tested image only when you choose.

## Flags

| Flag | Meaning |
| --- | --- |
| `--version <version>` | The built version to mirror. Required. |
| `--tenant` / `--environment` | Target a specific tenant/environment instead of the current scope. |
| `--dry-run` | Resolve and print the `imagetools create` copy commands without executing them. |

## Example

```
# Iterate: build in the remote-agent env, deploy to the local cluster registry.
# When happy, publish the tested image to the shared registry for teammates:
erun publish acme dev --version 1.4.2
```
