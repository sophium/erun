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

Every release-tagged image is multi-architecture. `erun build --release` refuses to publish a single-arch artifact: after each per-arch `docker push`, it verifies the manifest list contains both `linux/amd64` and `linux/arm64` entries. A missing entry aborts the build.

The check applies whether the build is local (in which case `docker buildx imagetools inspect <tag>` is the verification call) or run inside the runtime pod (where the same check runs against the pushed registry copy).

## Lifecycle algorithm

`erun release` is version paperwork only — it never builds, publishes, or verifies an artifact:

1. Resolve the canonical version from `VERSION`. Match the regex; abort with `INVALID_VERSION` on miss.
2. Refuse to proceed if the working tree has uncommitted changes (`git status --porcelain` non-empty). Code: `DIRTY_WORKTREE`.
3. Check the release tag is not already present in git (`git rev-parse v<version>`). Code: `TAG_CONFLICT`. Override available via `--force` (deletes the prior tag first).
4. Sync `<chart>/Chart.yaml`'s `version` and `appVersion` fields to `<version>`. Commit on `release.mainbranch`.
5. If **stable**: update package-manager metadata. Commit alongside the chart sync.
6. Create the release tag **locally**.
7. `git push` the release tag.
8. If **stable**: sync the package-manager checksums against the now-public source archive and commit.
9. If **stable**: open a follow-up commit that bumps the canonical `VERSION` to the next patch (`X.Y.Z+1`), merge to `release.developbranch`, and `git push --follow-tags` both branches. A rejected branch push is retried up to twice, each time after `git fetch origin <branch>` + `git rebase FETCH_HEAD`; the retry also pushes `v<version>` by name, because the rebase rewrites the commit `--follow-tags` was tracking. A rebase that cannot apply is aborted (`git rebase --abort`) and the push's own error is reported as `GIT_PUSH_FAILED`.
10. Exit `0`.

`erun release` exits `0` having published nothing, whether it succeeds or fails partway through. A failed release is still a release: the tag it did manage to create (if any) is a permanent, honest record of the source it tried to release. Version numbers are cheap and monotonic — a tag whose artifacts never landed is simply a dead version, and `erun deploy` never builds, so a dead version is not deployable by accident. Fix the issue and release again; a re-run reuses the existing tag when it already matches HEAD.

## Building and publishing the released version

Building that version's images and charts, and verifying they resolve, is `erun build --release`'s job — reached only via that command (or `erun push --version <version>` to republish an already-built version):

1. It runs `erun release`'s own steps 1–9 above first (a failed build afterward still leaves an honest tag naming the source it tried to build).
2. Re-reads the base branch from origin (`git fetch origin <branch>`, then `git rev-list --count HEAD..FETCH_HEAD`). A non-zero count means the branch moved after step 4 above rebased onto it, and the build aborts with `BASE_BRANCH_MOVED` before it spends anything. A remote that cannot be read is not an answer: the count is skipped and the build proceeds.
3. Checks the docker daemon's node has room for a multi-arch build (prunes reclaimable build cache first; refuses with `INSUFFICIENT_DISK_HEADROOM` when free space is observably below a floor). An inconclusive read (the daemon lives in a separate container's filesystem) is not a refusal.
4. Refuses up front if the release stamps an image nothing in this run would publish — usually `erun build --release` run from inside one component's build directory rather than the project root. Code: `UNPUBLISHABLE_RELEASE_IMAGE`.
5. Refuses if no credential resolves for a ghcr.io registry it would publish to at all (no docker config entry, no gh session, no `GH_TOKEN`/`GITHUB_TOKEN`). GHCR never accepts an anonymous push, so this is refused before the build. Code: `REGISTRY_CREDENTIAL_MISSING`.
6. Builds per-arch images and pushes to the registry. Assembles the manifest list. Verifies multi-arch coverage (see above). Publishes every co-located helm chart and reads each one back.
7. Verifies, for any erun image a Terraform module references, that it resolves with no credential at all. Code on failure: `ANONYMOUS_PULLABILITY_FAILED`.
8. Reports the released version. Exit `0`.

Because the tag already exists once building starts, none of steps 2–8 failing leaves the repository in an inconsistent state the way it would if the tag were created after them — it just means the version is tagged but not (yet) deployable. Fix the issue and run `erun build --release` again.

## Error codes

| Code | Cause | Exit code |
|---|---|---|
| `INVALID_VERSION` | Canonical `VERSION` fails the version regex. | `1` |
| `DIRTY_WORKTREE` | Uncommitted changes in the working tree. | `1` |
| `TAG_CONFLICT` | Release tag already exists in git at a different commit than HEAD. | `1` |
| `GIT_PUSH_FAILED` | `git push --follow-tags` failed and the bounded rebase-and-retry could not absorb it (network / permission, or a rebase that does not apply). | `2` |
| `PACKAGE_METADATA_WRITE_FAILED` | Homebrew formula / Scoop manifest write failed. Rerun `--dry-run` to inspect, fix manually. | `2` |

`erun build --release` additionally reaches:

| Code | Cause | Exit code |
|---|---|---|
| `BASE_BRANCH_MOVED` | `origin/<release.mainbranch>` gained commits after the release rebased onto it. Refused before the build; nothing is published. Recover with `git pull --rebase origin <branch>` then `erun build --release --force` (`--force` recreates the local tag the rebase leaves behind). | `1` |
| `INSUFFICIENT_DISK_HEADROOM` | The docker root has less free space than the configured floor for a multi-arch release build. | `1` |
| `UNPUBLISHABLE_RELEASE_IMAGE` | The release stamps an image no build in this run publishes — usually `erun build --release` run from inside one component's build directory. Refused during resolution, before any stage runs. | `1` |
| `REGISTRY_CREDENTIAL_MISSING` | No credential resolves for a ghcr.io registry the release would publish to at all. | `1` |
| `MULTI_ARCH_VERIFY_FAILED` | Manifest list missing `linux/amd64` or `linux/arm64`. | `2` |
| `REGISTRY_PUSH_AUTH_FAILED` | Registry rejected the push after one interactive-login retry. | `2` |
| `ANONYMOUS_PULLABILITY_FAILED` | An erun image a Terraform module references does not resolve with no credential at all. | `2` |

In every case, `--dry-run` reports the planned steps without executing them.

## See also

- [`erun release`](/cli/release) — Operator-facing workflow.
- [`erun build`](/cli/build) — produces the snapshot tags that this command does **not** accept.
- [Deployment · Release flow](/deployment/release-flow) — multi-arch + fingerprint-cache reasoning.
- [Conventions spec · Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) — what `--force` bypasses.
