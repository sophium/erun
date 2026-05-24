---
title: erun release
---

# `erun release`

Plan and execute a project release. `erun release` is repository-wide: it moves all modules together — `erun-cli`, `erun-common`, `erun-mcp`, `erun-ui`, and `erun-devops` — and produces a single, tagged release artifact set.

## Synopsis

```
erun release [flags]
```

## What it does

1. Resolves the current version from `erun-devops/VERSION`.
2. Updates the chart `version` and `appVersion` to match.
3. Updates package-manager metadata (Homebrew formula, Scoop manifest, etc.) when present.
4. Creates the release commit and tag.
5. Builds release-tagged Docker images for `linux/amd64` and `linux/arm64`, pushes per-arch tags, and assembles manifest lists.
6. Prepares the next patch version for subsequent work.

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Resolve and print every step without performing any side effects. |

Most callers don't run `erun release` directly — it's invoked by CI when a release-tagged commit lands on `main`. Use `--dry-run` locally to inspect the plan before signing off.

## Stable vs candidate releases

- **Stable releases** use bare semver tags (`1.0.76`) and follow the full chart/metadata sync flow.
- **Candidate releases** use suffixed tags (`1.0.76-rc.1`) and skip the package-manager updates.

The behavior is selected by the release version string, not by a flag.

## Multi-arch contract

Every release-tagged image is multi-architecture. The release flow refuses to publish a single-arch artifact — the architecture coverage is a release-gate invariant. See [Release flow](/deployment/release-flow) for the full architecture and fingerprint-cache reasoning.
