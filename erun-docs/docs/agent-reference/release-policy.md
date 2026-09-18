---
title: Release version policy
---

# Release version policy

> For the Operator view, see [`erun release`](/cli/release).

The release version string carried in `<projectroot>/<tenant>-devops/VERSION` (or its sub-image overrides) determines whether `erun release` treats the release as **stable** or **candidate**, and whether package-manager metadata is touched. There is no separate flag — the version string itself is the gate.

## Version-string grammar

Every version is matched against this regular expression:

```
^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$
```

A version is rejected before any side effect when it fails the match.

| Capture | Constraint |
|---|---|
| Major | One or more digits. |
| Minor | One or more digits. |
| Patch | One or more digits. |
| Pre-release identifier (`-…`) | Optional. When present: one or more dot-separated identifiers, each `[A-Za-z0-9-]+`. |

## Stable vs candidate

| Class | Version pattern | Examples | Behaviour |
|---|---|---|---|
| **Stable** | No hyphen suffix. | `1.0.76`, `2.4.0`, `10.0.0` | Full release flow. Chart `version` + `appVersion` synced. Homebrew formula + Scoop manifest + any other registered package-manager metadata updated and committed. Release tag created on `release.mainbranch`. `release.developbranch` is advanced to the next patch. |
| **Candidate** | Hyphen suffix present. | `1.0.76-rc.1`, `1.0.76-beta.2`, `2.5.0-canary`, `3.0.0-alpha.7` | Package-manager metadata **not** touched. Release tag created. No bump on `release.developbranch`. |
| **Rejected** | Fails the regex. | `1.0`, `latest`, `foo-bar`, `1.0.76-` | `erun release` aborts before any git or registry side effect with code `INVALID_VERSION`. |

`<X.Y.Z>-snapshot-<UTC-timestamp>` tags emitted by `erun build` in an agent env are **candidate-shaped** by this rule, but in practice they never reach `erun release` — they are produced by `erun build` and are not promotable through this command.

## Resolution order for the release version

1. The `VERSION` file at `<projectroot>/<tenant>-devops/VERSION` (canonical for the release).
2. Any per-image `<projectroot>/<tenant>-devops/docker/<image>/VERSION` overrides — these stay independent of the canonical version and pin the corresponding image to its own version line.

`erun release` reads only the canonical file; image-level overrides are not promotable.

## Multi-arch contract

Every release-tagged image is multi-architecture. The build pipeline refuses to publish a single-arch artifact: after each per-arch `docker push`, `erun build --release` verifies the manifest list contains both `linux/amd64` and `linux/arm64` entries. A missing entry aborts the build, which is the step that publishes. `erun release` never publishes and so never performs this check.

The check applies whether the build is local (in which case `docker buildx imagetools inspect <tag>` is the verification call) or run inside the runtime pod (where the same check runs against the pushed registry copy).

## Lifecycle algorithm

1. Resolve the canonical version from `VERSION`. Match the regex; abort with `INVALID_VERSION` on miss.
2. Refuse to proceed if the working tree has uncommitted changes (`git status --porcelain` non-empty). Code: `DIRTY_WORKTREE`.
3. Check the release tag is not already present in git (`git rev-parse v<version>`) or the registry (`docker manifest inspect <registry>/<image>:<version>` for each image in the deploy plan). Code: `TAG_CONFLICT`. Override available via `erun build --release --force` (deletes the prior tag first).
4. Sync `<chart>/Chart.yaml`'s `version` and `appVersion` fields to `<version>`. Commit on `release.mainbranch`.
5. If **stable**: update package-manager metadata. Commit alongside the chart sync.
6. Create the release tag **locally**. Nothing is public yet.
7. `git push` the release tag. If **stable**: sync the package-manager checksums against the now-public source archive and commit.
8. If **stable**: open a follow-up commit that bumps the canonical `VERSION` to the next patch (`X.Y.Z+1`), merge to `release.developbranch`, and `git push --follow-tags` both branches. A rejected branch push is retried up to twice, each time after `git fetch origin <branch>` + `git rebase FETCH_HEAD`; the retry also pushes `v<version>` by name, because the rebase rewrites the commit `--follow-tags` was tracking. A rebase that cannot apply is aborted (`git rebase --abort`) and the push's own error is reported as `GIT_PUSH_FAILED`.
9. Exit `0`.

There is no build and no publish in that sequence. Everything before step 7 leaves nothing public, and the canonical `VERSION` still holds the version being released, so re-running retries the same version. From step 7 the tag is public — and that is all it is: a record of the source that was attempted, never a claim that artifacts exist for it. `erun deploy` never builds, so a tag whose artifacts were never produced names a dead version rather than corrupting anything; the remedy is to fix the source and release again.

`erun build --release` composes this same stamp/tag work with the build it owns: it refuses a version whose resolved images nothing in the run would publish, re-reads the base branch from origin and checks the node has room immediately before it spends anything (aborting `BASE_BRANCH_MOVED` before the build starts), builds and pushes every image and chart the version resolves, verifies each published manifest resolves, and only then reports the released version. A base branch that moves while *that* is in flight is answered at both ends: the pre-spend check refuses a move it can still see cheaply, and the final push absorbs one that lands during the build.

## Error codes

| Code | Raised by | Cause | Exit code |
|---|---|---|---|
| `INVALID_VERSION` | `erun release` | Canonical `VERSION` fails the version regex. | `1` |
| `DIRTY_WORKTREE` | `erun release` | Uncommitted changes in the working tree. | `1` |
| `TAG_CONFLICT` | `erun release` | Release tag already exists in git or in the registry. | `1` |
| `GIT_PUSH_FAILED` | `erun release` | `git push --follow-tags` failed and the bounded rebase-and-retry could not absorb it (network / permission, or a rebase that does not apply). Rerun `erun release` to complete the ref push. | `2` |
| `PACKAGE_METADATA_WRITE_FAILED` | `erun release` | Homebrew formula / Scoop manifest write failed. Rerun `--dry-run` to inspect, fix manually. | `2` |
| `UNPUBLISHABLE_RELEASE_IMAGE` | `erun build --release` | The version stamps an image no build in this run publishes — usually a build run from inside one component's build directory. Refused during resolution, before any stage runs. | `1` |
| `REGISTRY_CREDENTIAL_MISSING` | `erun build --release` | No credential resolves for a ghcr.io registry the version would publish to at all (no docker config entry, no gh session, no `GH_TOKEN`/`GITHUB_TOKEN`). GHCR never accepts an anonymous push, so this is refused before the build rather than at the push. | `1` |
| `BASE_BRANCH_MOVED` | `erun build --release` | `origin/<release.mainbranch>` gained commits after the release rebased onto it, so the run refuses rather than building on a stale base. Nothing is published and the canonical `VERSION` is untouched. Recover with `git pull --rebase origin <branch>` then `erun build --release --force` (`--force` recreates the local tag the rebase leaves behind). | `1` |
| `MULTI_ARCH_VERIFY_FAILED` | `erun build --release` | Manifest list missing `linux/amd64` or `linux/arm64`. The release tag is **not** pushed. | `2` |
| `PUBLISHED_ARTIFACT_UNRESOLVABLE` | `erun build --release` | A just-pushed image manifest did not resolve on read-back. The release tag is **not** pushed. | `2` |
| `REGISTRY_PUSH_AUTH_FAILED` | `erun build --release` | Registry rejected the push after one interactive-login retry. The release tag is **not** pushed. | `2` |

The codes are split because the work is: `erun release` can only fail on source control, and every registry or build failure belongs to `erun build --release`, which is the command that touches a registry.

In every case, `--dry-run` reports the planned steps without executing them.

## See also

- [`erun release`](/cli/release) — Operator-facing workflow.
- [`erun build`](/cli/build) — produces the snapshot tags that this command does **not** accept.
- [Deployment · Release flow](/deployment/release-flow) — multi-arch + fingerprint-cache reasoning.
- [Conventions spec · Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) — what `--force` bypasses.
