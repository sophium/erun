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

`erun release` is version paperwork only — it never builds, publishes, or verifies an artifact.

1. Resolves the current version from `erun-devops/VERSION`.
2. Updates the chart `version` and `appVersion` to match.
3. Updates package-manager metadata (Homebrew formula, Scoop manifest, etc.) when present.
4. Creates the release commit and a **local** tag.
5. Pushes the tag to origin.
6. Syncs packaging checksums against the now-public source archive.
7. Prepares the next patch version.
8. Pushes the branches. A branch that moved while the release was running is absorbed here: the push rebases the release's own generated commits onto it and retries, rather than failing.

That's it — `erun release` exits 0 having published nothing, whether it succeeds or fails partway through. **A failed release is still a release**: the tag is a permanent, honest record of the source it tried to release, and the remedy for a failed build or publish afterward is simply to fix the issue and release again.

Building and publishing that version's images and charts is a separate step, reached only via [`erun build --release`](/cli/build) (which stamps and tags the version the same way internally, then builds and publishes) or `erun push --version <version>` (to republish a version that is already built).

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Resolve and print every step without performing any side effects. |
| `--force` | Delete and recreate a conflicting release tag before tagging. |

Most callers don't run `erun release` directly — it's invoked by CI when a release-tagged commit lands on `main`. Use `--dry-run` locally to inspect the plan before signing off.

## Stable vs candidate releases

The version string itself decides — no separate flag. Plain semver (`1.0.76`, `2.4.0`) is **stable** and triggers the full flow including package-manager metadata. Semver with a hyphen suffix (`1.0.76-rc.1`, `1.0.76-beta.2`, `2.5.0-canary`) is a **candidate**: package-manager metadata untouched.

For the exact regex and the per-class behaviour, see [Agent reference · Release version policy](/agent-reference/release-policy).

## Error behaviour

`erun release` is atomic-ish: each failure aborts the release and tries hard to leave git and package-manager files unchanged. Common abort causes: dirty working tree, tag conflict, git-push failure mid-flow. Use `--dry-run` first when you're unsure of state. Full failure-code + recovery table: [Agent reference · Release version policy · Error codes](/agent-reference/release-policy#error-codes).

## Building and publishing the released version

`erun build --release` runs release's own stamp/commit/tag steps first — so a failed build afterward still leaves an honest tag naming the source it tried to build — then two preflights immediately before the build spends anything (the base branch has not moved since the release rebased onto it; the node has room for a multi-arch build), then builds and publishes every image and chart the version resolves, both `linux/amd64` and `linux/arm64`. Once publish succeeds it verifies that any erun image a Terraform module references resolves with no credential at all, before reporting the released version.

A release that cannot build or publish is not corrupt — version numbers are cheap and monotonic, and `erun deploy` never builds, so a dead version is not deployable by accident. Fix the issue and run `erun build --release` again; the existing tag is reused when it already matches HEAD.
