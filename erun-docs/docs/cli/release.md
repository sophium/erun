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

`erun release` orchestrates the pure primitives — it composes **build → push → git-tag** and reuses [`erun push`](/cli/push) for *all* publishing, including the runtime helm chart. It does not have a separate chart-publishing step of its own; the chart is published the same way for a release as for any other pushed version.

1. Resolves the current version from `erun-devops/VERSION`.
2. Updates the chart `version` and `appVersion` to match.
3. Updates package-manager metadata (Homebrew formula, Scoop manifest, etc.) when present.
4. Builds the release-tagged Docker images for `linux/amd64` and `linux/arm64`.
5. Runs `push` at the release version: pushes the per-arch image tags, assembles the multi-arch manifest list, and publishes + verifies the runtime helm chart.
6. Creates the release commit and tag.
7. Prepares the next patch version for subsequent work.

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Resolve and print every step without performing any side effects. |

Most callers don't run `erun release` directly — it's invoked by CI when a release-tagged commit lands on `main`. Use `--dry-run` locally to inspect the plan before signing off.

## Stable vs candidate releases

The version string itself decides — no separate flag. Plain semver (`1.0.76`, `2.4.0`) is **stable** and triggers the full flow including package-manager metadata. Semver with a hyphen suffix (`1.0.76-rc.1`, `1.0.76-beta.2`, `2.5.0-canary`) is a **candidate**: images and chart published, package-manager metadata untouched. Anything that fails the version grammar is rejected before any side effect.

For the exact regex, the per-class behaviour, and the multi-arch contract, see [Agent reference · Release version policy](/agent-reference/release-policy).

## Error behaviour

`erun release` is atomic-ish: each failure aborts the release and tries hard to leave git, the registry, and package-manager files unchanged. Common abort causes: dirty working tree, tag conflict, single-arch image, registry auth, git-push failure mid-flow. Use `--dry-run` first when you're unsure of state. Full failure-code + recovery table: [Agent reference · Release version policy · Error codes](/agent-reference/release-policy#error-codes).
