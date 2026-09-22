---
title: erun release
---

# `erun release`

Mark a project release in source control. `erun release` is repository-wide: it moves all modules together — `erun-cli`, `erun-common`, `erun-mcp`, `erun-ui`, and `erun-devops` — and commits and tags them as one version.

## Synopsis

```
erun release [flags]
```

## What it does

`erun release` is version paperwork. It resolves the version, stamps it into charts and package-manager metadata, commits it, tags it locally, pushes the tag and the branches, and prepares the next patch. **It never builds, publishes, or verifies an artifact.**

1. Resolves the current version from `erun-devops/VERSION`.
2. Updates the chart `version` and `appVersion` to match.
3. Updates package-manager metadata (Homebrew formula, Scoop manifest, etc.) when present.
4. Creates the release commit and a **local** tag.
5. Re-reads the base branch on origin, so a branch that moved is absorbed before the tag and branch pushes go out.
6. Pushes the tag, syncs packaging checksums against the now-public source archive, prepares the next patch version, and pushes the branches.

It exits 0 having published nothing, and says so. Artifact production belongs to [`erun build --release`](/cli/build), which composes this same stamp/tag work with the build and the publish, and to [`erun push --version <version>`](/cli/push) for a version that is already built. Neither is a step of `erun release`.

**A tag whose artifacts never landed is not corruption.** It names a dead version, and a dead version is not deployable by accident: `erun deploy` never builds. If the release's source turns out to be wrong, fix it and release again — version numbers are cheap and monotonic. Deferring the tag until a publish succeeds is deliberately not what happens here; see [Agent reference · Release version policy](/agent-reference/release-policy).

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Resolve and print every step without performing any side effects. |
| `--force` | Delete and recreate a conflicting release tag before tagging. |

Use `--dry-run` locally to inspect the plan before signing off.

## Stable vs candidate releases

The version string itself decides — no separate flag. Plain semver (`1.0.76`, `2.4.0`) is **stable** and triggers the full flow including package-manager metadata. Semver with a hyphen suffix (`1.0.76-rc.1`, `1.0.76-beta.2`, `2.5.0-canary`) is a **candidate**: package-manager metadata untouched. Anything that fails the version grammar is rejected before any side effect.

For the exact regex and the per-class behaviour, see [Agent reference · Release version policy](/agent-reference/release-policy).

## Error behaviour

`erun release` aborts on a dirty working tree, a tag conflict, or a git-push failure mid-flow, leaving git and package-manager files as it found them. Use `--dry-run` first when you're unsure of state. Full failure-code + recovery table: [Agent reference · Release version policy · Error codes](/agent-reference/release-policy#error-codes).

A base branch that someone else moved is handled at whichever end it lands: moved before the pushes, the release refuses and names the branch; moved during them, the final push rebases onto it and retries.

A failure *after* the tag reaches origin — a packaging-checksum sync, or a branch push whose rebase-and-retry could not apply — is recoverable by re-running `erun release`, which picks up where it stopped. Publishing the version's artifacts is a separate act, [`erun build --release`](/cli/build), and is not something a failed release has half-done.
