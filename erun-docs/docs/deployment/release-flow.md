---
title: Release flow
---

# Release flow

ERun's release flow is repository-wide: a release moves all modules together (`erun-cli`, `erun-common`, `erun-mcp`, `erun-ui`, `erun-devops`).

## Versioning

- `erun-devops/VERSION` is the canonical product version.
- Chart `version` and `appVersion` are kept in sync with this file at release time.
- A version is a content identity minted only by [`erun build`](/cli/build): a plain build mints `<base>-snapshot-<UTC-timestamp>`, and `--release` (used by `erun release`) pins the bare semver. The env type does not decide which kind is minted.

## Release-tagged images

`erun release` builds release-tagged images for both `linux/amd64` and `linux/arm64`, then hands the version to [`erun push`](/cli/push), which pushes the per-arch tags and assembles a manifest list so `<image>:<version>` resolves to either arch automatically. Release does not push directly — it reuses `push` for every publish, so a released version and a pushed snapshot are published the same way.

Base images (`erun-ubuntu`, `erun-dind`, `erun-ubuntu`-derived) publish first; dependent images publish only after their bases are available in the registry.

## Published runtime chart

Chart publishing belongs to [`erun push`](/cli/push), not to a release-only step: push publishes the runtime image **and** the canonical `erun-devops` helm chart together at the same version. It runs `helm package` + `helm push` to `oci://<registry>/charts`, so the chart is addressable as `oci://<registry>/charts/erun-devops:<version>` (default registry `ghcr.io/sophium`), then verifies the artifact with a `helm pull` round-trip before the push is considered complete. The chart's `version` and `appVersion` equal the pushed version — image and chart are one contract.

Because push does this for **every** version — snapshot or release — there is no chart-versus-image gap: any pushed version is deployable, not just released ones. `erun release` gets its chart published for free by reusing push; a snapshot pushed during iteration is just as deployable as a release.

Environments without a repo-local runtime chart deploy this artifact directly — see [`erun deploy` · Where the runtime chart comes from](/cli/deploy#where-the-runtime-chart-comes-from).

## Build caching

Release-tagged builds participate in the same content-fingerprint cache as snapshot builds. Fresh clones promote pinned bases without rebuilding; local Dockerfile edits trigger a rebuild because the recomputed fingerprint diverges.

See [Conventions · Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) for the full mechanism.

## Branch model

`erun release` operates against two project-configured branches:

| Field | Default | Role |
|---|---|---|
| `release.mainbranch` (`.erun/config.yaml`) | `main` | The branch that holds released versions. Release tags are created here. |
| `release.developbranch` (`.erun/config.yaml`) | `develop` | The branch where the next development cycle continues. The post-release "bump to next patch version" lands here. |

Override either in `.erun/config.yaml`:

```yaml
release:
  mainbranch: production
  developbranch: trunk
```

The conventional flow on a release:

1. CI sees a release-tagged commit land on `release.mainbranch`.
2. `erun release` reads `<projectroot>/<tenant>-devops/VERSION`, syncs the chart `version` / `appVersion`, builds the multi-arch images, runs `push` at the release version (per-arch tags + manifest list + the runtime chart), creates the release commit + tag, then advances the next patch on `release.developbranch`.
3. A subsequent `erun deploy <env> --version <released version>` against a runtime env installs the now-published image and chart from the registry by reference.

For projects that use a single-branch trunk model, set both fields to the same branch.
