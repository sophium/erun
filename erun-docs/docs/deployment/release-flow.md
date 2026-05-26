---
title: Release flow
---

# Release flow

ERun's release flow is repository-wide: a release moves all modules together (`erun-cli`, `erun-common`, `erun-mcp`, `erun-ui`, `erun-devops`).

## Versioning

- `erun-devops/VERSION` is the canonical product version.
- Chart `version` and `appVersion` are kept in sync with this file at release time.
- Builds in an **agent env** append `-snapshot-<UTC-timestamp>` to the version. **Runtime envs** use the bare semver.

## Release-tagged images

`erun build --release` produces multi-arch images for both `linux/amd64` and `linux/arm64`, pushes per-arch tags, and assembles a manifest list so `<image>:<version>` resolves to either arch automatically.

Base images (`erun-ubuntu`, `erun-dind`, `erun-ubuntu`-derived) publish first; dependent images publish only after their bases are available in the registry.

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
2. `erun release` reads `<projectroot>/<tenant>-devops/VERSION`, syncs the chart `version` / `appVersion`, creates the release commit + tag, builds and pushes the multi-arch images, then advances the next patch on `release.developbranch`.
3. A subsequent `erun deploy` against a runtime env pulls the now-published version from the registry.

For projects that use a single-branch trunk model, set both fields to the same branch.
