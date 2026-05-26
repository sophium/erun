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
| **Candidate** | Hyphen suffix present. | `1.0.76-rc.1`, `1.0.76-beta.2`, `2.5.0-canary`, `3.0.0-alpha.7` | Images and chart published; package-manager metadata **not** touched. Release tag created. No bump on `release.developbranch`. |
| **Rejected** | Fails the regex. | `1.0`, `latest`, `foo-bar`, `1.0.76-` | `erun release` aborts before any git or registry side effect with code `INVALID_VERSION`. |

`<X.Y.Z>-snapshot-<UTC-timestamp>` tags emitted by `erun build` in an agent env are **candidate-shaped** by this rule, but in practice they never reach `erun release` — they are produced by `erun build` and are not promotable through this command.

## Resolution order for the release version

1. The `VERSION` file at `<projectroot>/<tenant>-devops/VERSION` (canonical for the release).
2. Any per-image `<projectroot>/<tenant>-devops/docker/<image>/VERSION` overrides — these stay independent of the canonical version and pin the corresponding image to its own version line.

`erun release` reads only the canonical file; image-level overrides are not promotable.

## Multi-arch contract

Every release-tagged image is multi-architecture. The release pipeline refuses to publish a single-arch artifact: after each per-arch `docker push`, `erun release` verifies the manifest list contains both `linux/amd64` and `linux/arm64` entries. A missing entry aborts the release before the tag is created on git.

The check applies whether the build is local (in which case `docker buildx imagetools inspect <tag>` is the verification call) or run inside the runtime pod (where the same check runs against the pushed registry copy).

## Lifecycle algorithm

1. Resolve the canonical version from `VERSION`. Match the regex; abort with `INVALID_VERSION` on miss.
2. Refuse to proceed if the working tree has uncommitted changes (`git status --porcelain` non-empty). Code: `DIRTY_WORKTREE`.
3. Check the release tag is not already present in git (`git rev-parse v<version>`) or the registry (`docker manifest inspect <registry>/<image>:<version>` for each image in the deploy plan). Code: `TAG_CONFLICT`. Override available via `erun build --release --force` (deletes the prior tag first).
4. Sync `<chart>/Chart.yaml`'s `version` and `appVersion` fields to `<version>`. Commit on `release.mainbranch`.
5. If **stable**: update package-manager metadata. Commit alongside the chart sync.
6. Build per-arch images and push to the registry. Assemble the manifest list. Verify multi-arch coverage (see above).
7. `git push --follow-tags` to publish the release commit + tag.
8. If **stable**: open a follow-up commit on `release.developbranch` that bumps the canonical `VERSION` to the next patch (`X.Y.Z+1`). Push.
9. Exit `0`.

Each step is wrapped in an abort-and-rollback boundary: a failure at step N rolls back side effects at steps `< N` only if those steps were filesystem-local (chart sync revert, package-manager metadata revert). Registry pushes are not rolled back — the contract is that a partially-published release leaves a recoverable state (re-run `--dry-run` to inspect, then resume with explicit flags).

## Error codes

| Code | Cause | Exit code |
|---|---|---|
| `INVALID_VERSION` | Canonical `VERSION` fails the version regex. | `1` |
| `DIRTY_WORKTREE` | Uncommitted changes in the working tree. | `1` |
| `TAG_CONFLICT` | Release tag already exists in git or in the registry. | `1` |
| `MULTI_ARCH_VERIFY_FAILED` | Manifest list missing `linux/amd64` or `linux/arm64`. The release tag is **not** created on git. | `2` |
| `REGISTRY_PUSH_AUTH_FAILED` | Registry rejected the push after one interactive-login retry. | `2` |
| `GIT_PUSH_FAILED` | `git push --follow-tags` failed (network / permission). Local tag and registry images exist; rerun `git push --follow-tags` to complete. | `2` |
| `PACKAGE_METADATA_WRITE_FAILED` | Homebrew formula / Scoop manifest write failed after the registry push. Registry has the tag; rerun `--dry-run` to inspect, fix manually. | `2` |

In every case, `--dry-run` reports the planned steps without executing them.

## See also

- [`erun release`](/cli/release) — Operator-facing workflow.
- [`erun build`](/cli/build) — produces the snapshot tags that this command does **not** accept.
- [Deployment · Release flow](/deployment/release-flow) — multi-arch + fingerprint-cache reasoning.
- [Conventions spec · Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) — what `--force` bypasses.
