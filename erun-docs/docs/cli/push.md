---
title: erun push
---

# `erun push`

Publish a version's outputs to the configured container registry: the multi-arch image manifest **and** the runtime helm chart. `erun push --version <version>` is the publish step of the [delivery pipeline](/pipeline) — it takes a version [`erun build`](/cli/build) minted and makes it deployable.

## Synopsis

```
erun push --version <version> [flags]
```

The version is **required** and names what to publish. It's a content identity minted by `build`; `push` never mints one. There is no environment-type branch — push behaves the same everywhere.

## What push publishes

For the given version, `erun push`:

1. **Builds each image from its source context.** It resolves the current build context and builds per-arch images, promoting unchanged images straight from the [fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) (a clean clone publishes without rebuilding). It does **not** push a prebuilt bare tag — it always builds from source so what lands is reproducible.
2. **Pushes per-arch tags and assembles the multi-arch manifest list**, so `<registry>/<image>:<version>` resolves to either architecture automatically.
3. **Publishes the runtime helm chart** — `helm package` + `helm push` to `oci://<registry>/charts`, then a `helm pull` round-trip to verify the artifact landed. The chart's `version` and `appVersion` equal `<version>`, so image and chart are one contract at the same version.

Because push publishes the chart for every version — snapshot or release — any pushed version is deployable. There is no longer a gap where a snapshot image existed without a matching chart. [`erun release`](/cli/release) reuses `push` for all of this publishing; [`erun deploy`](/cli/deploy) only consumes what push produced.

## Flags

| Flag | Description |
|---|---|
| `--build` | **Operator shortcut** (build → push). Build the current source first, then push the version that build mints — so you don't have to copy the snapshot version out of `erun build` by hand. Mutually exclusive with `--version`. Programmatic callers (the desktop app, scripts, Agents over MCP) compose `build` and `push` themselves and thread the version; see [Command primitives](/concepts/command-primitives). |
| `--force` | Rebuild and re-push every image, bypassing the fingerprint cache. Also forces the `--build` step. |
| `--dry-run` | Resolve and print every `docker build` / `docker push` / `docker manifest` and `helm package` / `helm push` command without executing. |

## Registry resolution

The push target is the `build`-marked registry in the project's registry list; a project that marks no `build` registry cannot build or push. See [Configuration · Container registries](/reference/configuration#container-registries) for the list shape and role rules, and [Container registries](/deployment/registries) for setup notes per registry vendor.

## Examples

Mint a version with `build`, then publish everything at it:

```bash
erun build --output json     # prints {version, baseVersion, images} — capture the version
erun push --version 1.0.81-snapshot-20260616120000
```

Preview what a push would publish without executing:

```bash
erun push --version 1.0.81-snapshot-20260616120000 --dry-run
```

## Authentication

If the registry rejects the push as unauthorised, `erun push` retries automatically with an interactive `docker login` prompt; for GHCR, a scope-mismatch additionally triggers `gh auth refresh -s write:packages,read:packages`. Both retries require a TTY. Full retry-trigger pattern table: [Agent reference · CLI flag spec · `erun push` authentication](/agent-reference/cli-flags#erun-push).

## Error behaviour

| Failure | Behaviour |
|---|---|
| No `--version`. | Errors before any work: `push requires a version` — push publishes a specific version, it does not mint one. Exit code 1. |
| No build context to publish. | Errors; nothing is pushed. |
| Foreign-arch binfmt missing. | Fails before the per-arch build with a direct error. |
| Registry rejects the push as unauthorised. | Retries with `docker login` (and `gh auth refresh` for GHCR scope mismatches); both need a TTY. Without one, errors with the auth failure. |
| Chart `helm push` or the `helm pull` verification fails. | Errors after the images pushed; the version's images are published but its chart is not, so it is not yet deployable. Re-run `erun push --version <version>` once the registry/auth issue is fixed. |
