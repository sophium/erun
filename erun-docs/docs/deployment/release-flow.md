---
title: Release flow
---

# Release flow

ERun's release flow is repository-wide: a release moves all modules together (`erun-cli`, `erun-common`, `erun-mcp`, `erun-ui`, `erun-devops`).

## Versioning

- `erun-devops/VERSION` is the canonical product version.
- Chart `version` and `appVersion` are kept in sync with this file at release time.
- Snapshot builds in the `local` environment append `-snapshot-<UTC-timestamp>` to the version. Non-local environments use the bare semver.

## Release-tagged images

`erun build --release` produces multi-arch images for both `linux/amd64` and `linux/arm64`, pushes per-arch tags, and assembles a manifest list so `<image>:<version>` resolves to either arch automatically.

Base images (`erun-ubuntu`, `erun-dind`, `erun-ubuntu`-derived) publish first; dependent images publish only after their bases are available in the registry.

## Fingerprint cache

Every Docker build computes a content fingerprint over the Dockerfile and its COPY sources. The fingerprint is stored under `docker.fingerprints.<image>` in `.erun/config.yaml`. On the next build, ERun pulls `<image>:<VERSION>` from the registry and tags it locally with the fingerprint — if the local fingerprint matches, the image is promoted instead of rebuilt.

The result: fresh clones promote pinned bases without rebuilding, but local Dockerfile edits still trigger a rebuild because the recomputed fingerprint diverges.
