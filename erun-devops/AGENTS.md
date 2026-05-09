# AGENTS.md

Additional guidance for `erun-devops` and its subtree.

- Follow the repository root `AGENTS.md` first.
- This file adds DevOps-module-specific guidance for runtime images, charts, Linux packaging, and release behavior.

## Scope

- This module owns the shared DevOps runtime image, its base images, its Kubernetes runtime chart, and Linux packaging assets used during build and release flows.
- Treat the module as the operational runtime contract for build, open, deploy, and release behavior.

## Runtime Image Rules

- Treat `erun-ubuntu`, `erun-devops`, and `erun-dind` as one dependency graph. When a release image depends on another local image, preserve the dependency ordering so base images publish before dependents.
- Keep the runtime image thin over `erun-ubuntu`. Put shared OS package setup in `erun-ubuntu` when it is truly common, and keep `erun-devops` focused on DevOps tooling and runtime-specific wiring.
- Keep release-critical tool versions pinned in Dockerfiles. Do not switch to floating tags or unpinned downloads for kubectl, Helm, Terraform, Docker, gh, golangci-lint, or similar tooling.
- Prefer simple, reviewable Docker layer ordering. Put stable, expensive tooling installation layers before builder artifact copies so source changes do not invalidate them unnecessarily.
- Treat `ARG TARGETARCH` and multi-arch tooling downloads as cache boundaries. Per-arch caches are expected; do not assume `amd64` and `arm64` can reuse the same layer.
- Pin published base images by content via `docker.fingerprints` in `.erun/config.yaml`. The map is keyed by image name (e.g. `erun-ubuntu`) and stores the 16-character lowercase hex fingerprint that release CI computed when it last published `<image>:<VERSION>`. Before resolving incremental promotion, the build flow pulls `<image>:<VERSION>` from the registry and tags it locally as `<image>:fp-<fingerprint>-<arch>`; the standard fingerprint check then promotes the build instead of rebuilding it. Local Dockerfile edits diverge the locally-computed fingerprint from the configured one, the inspect misses, and the build rebuilds — exactly the desired safety net. Bump the configured fingerprint in the same commit that publishes a new base.

## Runtime Chart Rules

- Keep the shared runtime chart and any generated tenant chart contract in sync. The shared runtime is the template for tenant-specific DevOps modules, so deployment behavior must remain aligned across both.
- The runtime stack is split into per-pod Helm releases under `erun-devops/k8s/`:
  - `erun-devops` — the runtime pod (devops + dind sidecar). Always deployed; produces the shared `/home/erun` PVC, the docker daemon PVC, and the runtime ServiceAccount/RBAC.
  - `erun-backend-postgres` — opt-in. Owns the postgres Deployment + Service + PVC and the postgres password Secret (`erun-backend-postgres`). Created on first deploy and reused via `lookup` on subsequent deploys.
  - `erun-backend-db` — opt-in. Owns the migration Job. The Job is wired as a Helm `post-install,post-upgrade` hook with `helm.sh/hook-delete-policy: before-hook-creation` so each release run replaces the prior Job and migrations only run when this release is applied.
  - `erun-backend-api` — opt-in. Owns the API Deployment + Service. Consumes the postgres Secret via `secretKeyRef`; expects `erun-backend-db` to have run before the API rolls.
- The runtime pod contract is intentional:
  - the main `erun-devops` container uses `DOCKER_HOST=unix:///var/run/docker.sock`
  - the `erun-dind` sidecar provides the daemon
  - `/var/lib/docker` is persisted on the `erun-devops-docker` PVC
  - `/home/erun` is persisted on the `erun-devops-home` PVC
- Backend deployment is opt-in via `erun deploy --components=...`. Default `erun deploy` brings up only the runtime pod. The opt-in component names match the chart directory names (`erun-backend-postgres`, `erun-backend-db`, `erun-backend-api`).
- Charts deploy in dependency order regardless of `--components` input order. The order is configured per environment in `.erun/config.yaml` under `environments.<env>.k8s.deployments`. Each item is either a component name (deployed alone) or a list of names (deployed in parallel within that step). Different environments may declare different plans (e.g. dev runs a slim plan, prod runs the full backend). When an environment has no `k8s` section, deploy falls back to the default rank `erun-backend-postgres → erun-backend-db → erun-backend-api → other`. The `erun` repo's own `local` environment declares `[erun-devops, erun-backend-postgres]` as a parallel first step (they share no state), then `erun-backend-db`, then `erun-backend-api`.
- When all locally-built images for a chart were promoted from a cached fingerprint (no rebuild), `erun deploy` skips the helm command and the redundant push for that chart. `erun-backend-postgres` is a passthrough wrapper over a pinned `postgres:<VERSION>` (see `erun-devops/docker/erun-backend-postgres/VERSION`) so it participates in the same fingerprint-promote-and-skip path as the other backend components. Charts with no locally-built images at all would always deploy when included so chart-only changes still ship.
- Do not move build cache, Docker state, or home-directory state onto `emptyDir` unless the change is deliberate and the persistence tradeoff is documented in the code review and tests.
- Keep binfmt installation explicit for release builds. Multi-arch release support depends on the chart installing `amd64` and `arm64` emulation before the dind daemon is used.

## Build Workflow

- `erun build` should behave as one coherent workflow across transports and modules. This module provides the runtime assets consumed by that workflow rather than defining a separate local policy.
- All Docker builds shell out to plain `docker build` against the local daemon's BuildKit (the runtime image sets `DOCKER_BUILDKIT=1`). Docker 23+ routes `docker build` through the `docker-buildx` CLI plugin whenever BuildKit is enabled, so the runtime image installs the plugin into `/usr/local/lib/docker/cli-plugins/docker-buildx` as the BuildKit frontend driver. Cross-architecture builds rely on that plugin running against the local daemon plus binfmt — not on creating a separate buildx builder instance. BuildKit cannot be turned off because the Dockerfiles depend on `RUN --mount=type=cache,...`.
- Every `erun build`, `erun build --release`, and `erun deploy` invocation builds for both `linux/amd64` and `linux/arm64`. There is no single-platform code path. The reason: a single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at developer-machine build time, not at remote deploy time.
- The default platforms are set on every `DockerBuildSpec` at construction (`resolveDockerBuildSpec`/`ResolveDockerBuildForImageReference`). Per-command configurators only flip `Push` (false for plain `erun build`, true for `--release` and `deploy`) — they do not touch `Platforms`.
- Build execution iterates per platform: `docker build --platform <p> --provenance=false -t <image>:<version>-<arch>`, then `docker tag` to attach the fingerprint cache tag (`fp-<hash>-<arch>`). When `Push` is true, after both arches have built and fp-tagged, the per-arch tags are pushed individually and assembled into a manifest list with `docker manifest create --amend` + `docker manifest push`. `--provenance=false` is required so each per-arch tag is a plain image manifest the assembly step can consume.
- Cross-architecture builds rely on the daemon having binfmt/qemu installed for the foreign arch — the runtime chart installs that before dind starts. Per-platform results land in the daemon's local image store, which is what lets fingerprint promotion (`docker tag <fp-tag> <release-tag>-<arch>` then push + manifest assembly) skip the `docker build` on the next run.
- Multi-arch builds must verify the daemon can produce every required platform. Failure mode: a missing binfmt entry, surfaced as a direct error rather than a confusing per-arch build failure.
- BuildKit cache mounts (`RUN --mount=type=cache,…` in the Dockerfiles) persist in `/var/lib/docker/buildkit` on the `erun-devops-docker` PVC, so repeated builds in the same namespace reuse Go module / apt / npm caches without publishing cache artifacts to the registry.
- Fingerprint cache tags (`<image>:fp-<hash>-<arch>`) are content-derived (Dockerfile + COPY sources, `.dockerignore`/`.gitignore` applied) and per-platform. The hash itself is platform-independent so a single content edit invalidates both per-arch fp tags simultaneously. Both arches must be present locally for a build to promote without rebuild; if either arch is missing, both arches rebuild and both fp tags are re-applied.
- Dry-run output for build, release, and deploy should show the real `docker build`, `docker tag`, `docker push`, and `docker manifest` commands, not just a summary.
- Do not switch the build flow to `docker buildx build`, multi-arch single-shot builds, or a dedicated container-driver builder instance. The current per-platform `docker build` path is what makes incremental promotion work: results land in the daemon's image store where `docker tag <real-arch> <fp-tag>` can attach the fingerprint cache tag. A naive `buildx build --push` skips the local store and breaks promotion, and `--load` is single-platform only, so a multi-arch buildx flow ends up iterating per platform anyway — the same shape as today, with extra moving parts. The buildx plugin shipped in the runtime image is allowed only in its role as the BuildKit frontend for plain `docker build`.

## Release Workflow

- Stable release behavior for this module currently means:
  - release chart metadata is rewritten so chart version and application version match the release
  - package-manager metadata for supported installers is updated together with the release when present
  - release commits and tags are created before release-tagged Docker images are pushed
  - after a successful stable release, the next patch version is prepared for subsequent work
- Candidate releases use candidate version tags and still rely on the same shared release/build orchestration.
- When changing release behavior, validate the repository-wide flow, not only this subtree. At minimum run the release-sensitive suites in `erun-common`, `erun-cli`, and `erun-mcp`.

## Version and Metadata Rules

- Keep one canonical module version input and treat it as the source of truth for release-tagged runtime artifacts.
- Keep chart version and application version aligned during releases.
- If the release flow changes package-manager metadata, update both version references and checksums where required. Do not leave Homebrew or Scoop definitions partially updated.

## Testing Expectations

- When changing Dockerfiles, add or update tests that lock in the intended dependency ordering, cache behavior, or pinned tooling versions.
- When changing the chart, test both the shared runtime chart behavior and the tenant-generation contract when practical.
- When changing release/build behavior that affects this module, add regression tests at the layer that owns the behavior and keep transport-specific preview and trace expectations aligned.
