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
4. Creates the release commit and a **local** tag. Nothing is public yet.
5. Re-reads the base branch on origin. A branch that moved since the release rebased onto it refuses here — before the build spends anything, and while nothing is public.
6. Builds the release-tagged Docker images for `linux/amd64` and `linux/arm64`.
7. Runs `push` at the release version: pushes the per-arch image tags, assembles the multi-arch manifest list, and publishes + verifies every helm chart.
8. Re-resolves each published image's manifest from the registry, so the version is known deployable rather than assumed to be.
9. Pushes the tag, syncs packaging checksums against the now-public source archive, prepares the next patch version, and pushes the branches. A branch that moved while the build was running is absorbed here: the push rebases the release's own generated commits onto it and retries, rather than failing with the version already public.

`erun release` runs all nine steps. [`erun build --release`](/cli/build) runs the same sequence — it is the same execution, under the build command's output — and `erun push --version <version>` does 6–7 on its own for a version that is already built.

**The artifacts publish before the git refs move.** A release that exits 0 therefore means `erun deploy --version <version>` can resolve both the image and the chart at that version. A release that cannot publish fails at step 6–8, leaving no public tag, no GitHub release, and `erun-devops/VERSION` still holding the version it was releasing — so re-running retries the *same* version rather than skipping past it.

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Resolve and print every step without performing any side effects. |

Most callers don't run `erun release` directly — it's invoked by CI when a release-tagged commit lands on `main`. Use `--dry-run` locally to inspect the plan before signing off.

## Stable vs candidate releases

The version string itself decides — no separate flag. Plain semver (`1.0.76`, `2.4.0`) is **stable** and triggers the full flow including package-manager metadata. Semver with a hyphen suffix (`1.0.76-rc.1`, `1.0.76-beta.2`, `2.5.0-canary`) is a **candidate**: images and chart published, package-manager metadata untouched. Anything that fails the version grammar is rejected before any side effect.

For the exact regex, the per-class behaviour, and the multi-arch contract, see [Agent reference · Release version policy](/agent-reference/release-policy).

## Error behaviour

`erun release` is atomic-ish: each failure aborts the release and tries hard to leave git, the registry, and package-manager files unchanged. Common abort causes: dirty working tree, tag conflict, single-arch image, registry auth, git-push failure mid-flow. It also refuses up front when the images it would stamp are not all covered by a build it will publish — running it from inside a single component's build directory is the usual cause; run it from the project root. Use `--dry-run` first when you're unsure of state. Full failure-code + recovery table: [Agent reference · Release version policy · Error codes](/agent-reference/release-policy#error-codes).

A base branch that someone else moved is handled at whichever end it lands: moved before the build, the release refuses at step 5 and names the branch, so the cost is seconds rather than the whole build; moved during the build, the final push rebases onto it and retries. Either way the repository ends up recording the version the registry holds.

The remaining window it can't roll back is a failure *after* the tag reaches origin — a packaging-checksum sync, or a branch push whose rebase-and-retry could not apply. The version is deployable at that point; re-running `erun release` picks up where it stopped.
