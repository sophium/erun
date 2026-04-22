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
- Keep release-critical tool versions pinned in Dockerfiles. Do not switch to floating tags or unpinned downloads for kubectl, Helm, Terraform, Docker, Buildx, gh, golangci-lint, or similar tooling.
- Prefer simple, reviewable Docker layer ordering. Put stable, expensive tooling installation layers before builder artifact copies so source changes do not invalidate them unnecessarily.
- Treat `ARG TARGETARCH` and multi-arch tooling downloads as cache boundaries. Per-arch caches are expected; do not assume `amd64` and `arm64` can reuse the same layer.

## Runtime Chart Rules

- Keep the shared runtime chart and any generated tenant chart contract in sync. The shared runtime is the template for tenant-specific DevOps modules, so deployment behavior must remain aligned across both.
- The runtime pod contract is intentional:
  - the main `erun-devops` container uses `DOCKER_HOST=unix:///var/run/docker.sock`
  - the `erun-dind` sidecar provides the daemon
  - `/var/lib/docker` is persisted on the `erun-devops-docker` PVC
  - `/home/erun` is persisted on the `erun-devops-home` PVC
- Do not move build cache, Docker state, or home-directory state onto `emptyDir` unless the change is deliberate and the persistence tradeoff is documented in the code review and tests.
- Keep binfmt installation explicit for release builds. Multi-arch release support depends on the chart installing `amd64` and `arm64` emulation before the dind daemon is used.

## Build Workflow

- `erun build` should behave as one coherent workflow across transports and modules. This module provides the runtime assets consumed by that workflow rather than defining a separate local policy.
- Local single-platform builds use ordinary `docker build`.
- Release-tagged Docker builds use `docker buildx build` with:
  - builder `erun-multiarch`
  - platforms `linux/amd64,linux/arm64`
  - `--push`
- Keep the builder state on persistent local storage. The `docker-container` buildx builder should reuse the dind daemon state mounted from the runtime PVC so repeated builds in the same namespace can reuse cached layers without publishing cache artifacts to the registry.
- Dry-run output for build and release should show the real buildx commands, not just a summary.

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
