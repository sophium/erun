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

### Agent env (development)

`erun push` resolves the current build context, **rebuilds** the image with a fresh snapshot tag (`<semver>-snapshot-<UTC-timestamp>`), then pushes per-arch tags and assembles a multi-arch manifest list. `push` in an agent env is build+push.

### Runtime env (release)

`erun push` **skips the build step** and runs `docker push` directly against the tag `<registry>/<image>:<VERSION>`. It assumes the image already exists in the local docker daemon (typically produced by a prior `erun build` in an agent env).

This split exists because runtime envs use stable release tags from the `VERSION` file. Silently rebuilding and overwriting those tags would mutate release artifacts — `push` here is the explicit "promote what was built" step.

See [Environment types](/concepts/environment-types) for the full split between agent and runtime envs.

## Flags

| Flag | Description |
|---|---|
| `--force` | Rebuild and re-push every image, bypassing the fingerprint cache. Only meaningful in an agent env. |
| `--dry-run` | Resolve and print every `docker push` command without executing. |

## Registry resolution

The push target is the `build`-marked registry in the project's registry list; a project that marks no `build` registry cannot build or push. See [Configuration · Container registries](/reference/configuration#container-registries) for the list shape and role rules, and [Container registries](/deployment/registries) for setup notes per registry vendor.

## Examples

Push from an agent env (rebuilds + pushes):

```bash
erun push --dry-run
erun push
```

Push from a runtime env (after `erun build` has run):

```bash
erun build       # produces tagged images (native multi-arch or ./build.sh)
erun push        # docker push the tagged images
```

## Authentication

If the registry rejects the push as unauthorised, `erun push` retries automatically with an interactive `docker login` prompt; for GHCR, a scope-mismatch additionally triggers `gh auth refresh -s write:packages,read:packages`. Both retries require a TTY. Full retry-trigger pattern table: [Agent reference · CLI flag spec · `erun push` authentication](/agent-reference/cli-flags#erun-push).

## Error behaviour

| Failure | Behaviour |
|---|---|
| No build context / image to push. | Errors; nothing is pushed. |
| Registry rejects the push as unauthorised. | Retries with `docker login` (and `gh auth refresh` for GHCR scope mismatches); both need a TTY. Without one, errors with the auth failure. |
| Foreign-arch binfmt missing (agent-env rebuild). | Fails before the per-arch build with a direct error. |
