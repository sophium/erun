---
title: Versioning
slug: /versioning
---

# Versioning

Every image and chart ERun produces carries a version, and the version tells you two things: *what* it is (a stable release, a candidate, a throwaway build) and *where* it's allowed to go. You almost never type a version by hand — ERun generates it from one number in a file plus the branch you're on.

## Semantic versions

ERun versions are [semver](https://semver.org): `MAJOR.MINOR.PATCH`, e.g. `1.4.2`.

- **MAJOR** — a breaking change.
- **MINOR** — a backward-compatible feature.
- **PATCH** — a backward-compatible fix.

A version can carry a **pre-release suffix** after a hyphen — `1.4.2-rc.abc1234`. By semver rules a suffixed version sorts *below* the plain one, so `1.4.2-rc.1` and `1.4.2-snapshot-…` are both "less than" the stable `1.4.2`. ERun uses three suffixes (`-rc.`, `-pr.`, `-snapshot-`) to mark the non-stable kinds below; a plain `MAJOR.MINOR.PATCH` with no suffix is always the stable release.

<figure className="erun-hero-figure">
  <img src="/img/semver-anatomy.svg" alt="The version 1.4.2-rc.abc1234 broken into four labelled parts: MAJOR (1, breaking), MINOR (4, feature), PATCH (2, fix), and a charcoal pre-release pill (-rc.abc1234, marking a candidate, prerelease, or snapshot). A note reads: a stable release drops the suffix — just 1.4.2." />
  <figcaption>Each part of a version answers a question; the suffix marks anything that isn't a stable release.</figcaption>
</figure>

## The VERSION file

The base number lives in a `VERSION` file at the release module root (typically `<tenant>-devops/VERSION`). Builds resolve it by walking up from the build directory to the nearest `VERSION`, so a single project-wide file covers every component, but a component can carry its own.

`VERSION` always holds the **next** number to ship. Cutting a stable release tags the current number and then **bumps `VERSION` to the next patch** automatically — release `1.0.81`, and `VERSION` moves to `1.0.82` for the work that follows.

## main and develop

Two branches anchor the model (their names come from `.erun/config.yaml` → `release.mainbranch` / `release.developbranch`, defaulting to `main` and `develop`):

- **`main`** is the release branch. A release here cuts a **stable** version, tags it, publishes it, bumps `VERSION` to the next patch, and merges `main` back into `develop` so the bump propagates.
- **`develop`** is the integration branch. A release here is a **candidate** (`-rc.`) — published for testing, but package-manager metadata (Homebrew, Scoop) is only updated for stable releases.

Any other branch (a feature or bug branch) produces a **prerelease** (`-pr.`).

The release cycle flows between the two branches — work integrates on `develop`, `main` cuts the stable release, and the version bump plus the merge flow back to `develop`:

<figure className="erun-hero-figure">
  <img src="/img/versioning-branches.svg" alt="The release cycle across branches: a cyan-outlined 'develop — integration' box flows to a 'main — stable release' box, which publishes a charcoal pill 'tag v1.0.81 — published, immutable'. A dashed arrow loops from main back to develop, labelled 'bump VERSION to 1.0.82, merge back'." />
  <figcaption>Work integrates on <code>develop</code>; <code>main</code> cuts the stable release, then the bump and merge flow back.</figcaption>
</figure>

## How a version is generated

When `erun release` runs (or CI runs it for you), it reads the base number from `VERSION`, looks at the branch, and produces one of four kinds:

<figure className="erun-hero-figure">
  <img src="/img/version-kinds.svg" alt="A charcoal source pill holding the VERSION base number 1.0.81 fans out via four arrows to four cyan-outlined outcomes: on main, stable 1.0.81; on develop, candidate 1.0.81-rc.commit; on a feature or bug branch, prerelease 1.0.81-pr.commit; a local build, snapshot 1.0.81-snapshot-timestamp." />
  <figcaption>The same base number; the branch (or a plain local build) decides which kind comes out.</figcaption>
</figure>

| Where it runs | Kind | Example | Published as |
|---|---|---|---|
| `main` | stable | `1.0.81` | git tag `v1.0.81`, images, chart, package managers — immutable |
| `develop` | candidate | `1.0.81-rc.<commit>` | images + chart (no package-manager update) |
| feature / bug branch | prerelease | `1.0.81-pr.<commit>` | images + chart |
| a plain local `build` (no release) | snapshot | `1.0.81-snapshot-<timestamp>` | only if pushed; disposable, for iterating |

`<commit>` is the short commit hash; `<timestamp>` is the UTC build time. The first three come from [`erun release`](/cli/release); the snapshot is what a plain [`build`](/cli/build) stamps when you're iterating in a `local` environment (see the [Delivery pipeline](/pipeline)).

## Where next

- **[Delivery pipeline](/pipeline)** — where versions fit in the pipeline.
- **[`erun release`](/cli/release)** — the command that cuts a version.
- **[Release version policy](/agent-reference/release-policy)** — the exact version grammar, the regex, and the per-kind rules.
- **[Release flow](/deployment/release-flow)** — how CI drives releases end to end.
