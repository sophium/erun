# Declared once at the top so the test stage's own `helm` (for the
# erun-devops/k8s chart tests, run via `make check`'s helm-chart-tests target)
# and the final image's `helm` stay pinned to the same version instead of two
# independent defaults drifting apart.
ARG HELM_VERSION=v4.2.2

# Declared once at the top for the same reason as HELM_VERSION above: the test
# stage's own `atlas` (needed so `erun exec gate-merge`/`erun exec merge` can
# regenerate a conflicted erun-backend-db atlas.sum for real inside
# `make integration-test`, not just when a developer's own machine happens to
# have atlas installed) and the final image's `atlas` stay pinned to the same
# version.
ARG ATLAS_VERSION=v1.2.0

# Declared once at the top for the same reason as HELM_VERSION above: the test
# stage's own Playwright install-deps call (below) and the final image's
# install-deps call must stay pinned to the same version, so the installed
# Chromium runtime library set is identical whether the suite runs inside the
# build or inside a deployed runtime pod.
ARG PLAYWRIGHT_VERSION=1.60.0

# Pulled into the test stage purely to copy a pinned Node/Yarn toolchain out
# of — see the node COPY below. No RUN of its own, so it needs no emulation
# even though the test stage is pinned to BUILDPLATFORM (matches the `goroot`
# stage's reasoning further down).
# Pinned to $BUILDPLATFORM because its only consumer is the `test` stage below,
# which is itself $BUILDPLATFORM: this toolchain runs `make test-frontend` at
# build time and never ships in the final image. Left unpinned it resolves to
# the TARGET platform, so a multi-arch release copied an amd64 `node` into an
# arm64 stage and died with "running an x86 program on an arm64 OS". Only a
# release builds both arches, so a native build never showed it.
FROM --platform=$BUILDPLATFORM node:22.14-bookworm AS node

# Run the repository test gate inside the build, per the delivery-pipeline
# convention (docs /pipeline, "How you set it up — and where tests run"):
# a failing test fails the build and no image is tagged, so a green build
# is a tested build. Pinned to BUILDPLATFORM so the suite runs natively
# exactly once per build instead of once per target arch under emulation;
# the builder stage depends on this stage's marker, so no artefact ships
# untested while BuildKit still runs the two stages in parallel.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

WORKDIR /src

# dtach for erun-mcp's attach/PTY test suite (test-erun-mcp, below): those
# tests exec a real `dtach -A` session and fail with "dtach: not found" on
# the bare `golang` base this stage runs on. The final image already installs
# dtach for the runtime session machinery (see the apt-get layer further
# down); this stage needs its own copy since it is a separate, unrelated base.
#
# build-essential/pkg-config/libgtk-3-dev/libwebkit2gtk-4.1-dev/libsoup-3.0-dev
# are the same Wails/webkit CGO deps the final image installs below (see that
# layer's own comment), needed here so `test-playwright` (root Makefile) can
# `go build -tags desktop,production,webkit2_41` erun-app in this stage.
# Verified empirically against this exact base (golang:1.26.0, Debian
# trixie): the package set installs cleanly with no conflicts, and the
# resulting build succeeds and runs `--headless` with zero display (see root
# AGENTS.md's Integration Test Gate section).
RUN apt-get update && apt-get install -y --no-install-recommends \
        dtach build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev && \
    rm -rf /var/lib/apt/lists/*

# GOLANGCI_LINT_VERSION is the single checked-in source both this stage and the
# builder stage below install from, so the repo-root pin and the image's
# installed binary cannot drift the way a Dockerfile-only default did.
COPY GOLANGCI_LINT_VERSION /src/GOLANGCI_LINT_VERSION

# Build golangci-lint from source against this stage's Go toolchain — same
# reason as the builder stage: a prebuilt binary's embedded analyzer libraries
# (go/ast, go/types, go/parser) drift behind erun's go.mod Go version and abort
# with "go language version used to build golangci-lint is lower than the
# targeted Go version". Installed before the source COPYs (stable, expensive)
# so source changes do not invalidate this layer. Pinned repo-wide.
ARG GOLANGCI_LINT_VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-$(tr -d '\n' < /src/GOLANGCI_LINT_VERSION)}" && \
    GOBIN=/usr/local/bin go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"

# `helm` for `make check`'s helm-chart-tests target: the erun-devops/k8s
# chart tests are pure `helm template` assertions -- no cluster, no docker --
# so a pinned binary is all this bare golang image needs to run them. BUILDARCH
# is Docker's predefined build-host-arch arg; the stage is pinned to
# BUILDPLATFORM so it always matches the host actually running this RUN.
ARG BUILDARCH
ARG HELM_VERSION
RUN curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${BUILDARCH}.tar.gz" && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    install -m 0755 "/tmp/linux-${BUILDARCH}/helm" /usr/local/bin/helm && \
    rm -rf /tmp/helm.tar.gz "/tmp/linux-${BUILDARCH}"

# `atlas` for the integration suite's real gate-merge/merge atlas.sum
# regeneration scenarios (erun-integration/exec_test.go): those scenarios
# invoke the real `atlas migrate hash`/`atlas migrate validate` via
# ERUN_ATLAS_BIN, the same seam production code resolves it through, so the
# suite proves the checksum this fix relies on is genuinely tamper-sensitive
# rather than trusting a hand-rolled stub to reimplement atlas's own hashing.
ARG ATLAS_VERSION
RUN curl -fsSL -o /usr/local/bin/atlas "https://release.ariga.io/atlas/atlas-linux-${BUILDARCH}-${ATLAS_VERSION}" && \
    chmod 0755 /usr/local/bin/atlas

# Node/Yarn for `make test-frontend` (erun-kit + erun-console). Copied out of
# the pinned `node` stage above rather than apt/curl-installed, same pattern
# as `golangci-lint`/`goroot`: a pinned upstream image is the reproducible
# source, not a floating NodeSource script. `yarn@1.22.22` matches the
# `packageManager` field both workspace members pin.
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx && \
    npm install -g yarn@1.22.22

# Chromium runtime libraries for erun-ui/playwright, mirroring the final
# image's own install below (same reasoning, same pinned version) so
# `test-playwright` (root Makefile) can run the suite inside this stage, not
# only inside a deployed runtime pod. Needs `npx` from the Node/Yarn install
# just above. Verified empirically: Playwright's own install-deps recognizes
# and succeeds on this same Debian trixie base with no unsupported-platform
# warning (see root AGENTS.md's Integration Test Gate section).
ARG PLAYWRIGHT_VERSION
RUN npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium && \
    rm -rf /var/lib/apt/lists/*

COPY Makefile /src/Makefile
COPY scripts /src/scripts
COPY package.json yarn.lock /src/
COPY erun-cli /src/erun-cli
COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp
COPY erun-integration /src/erun-integration
# erun-devops/dns01-webhook has no LINT_MODULES entry (no golangci-lint config
# of its own) but does have a Makefile test target (test-erun-dns01-webhook),
# so it needs the same COPY-or-`cd`-fails treatment as the modules below.
COPY erun-devops/dns01-webhook /src/erun-devops/dns01-webhook
# Every module in the Makefile's LINT_MODULES must land here or `make check`
# cannot cd into it -- and so must every path those modules' tests read at run
# time, which is not the same set. erun-ui's build-stamp guard deliberately
# checks the package-manager formulae as well as the module's own scripts,
# because a stamp corrected only in build.sh still leaves every installed
# desktop unstamped; it reads them with os.ReadFile and treats a missing file as
# fatal, so omitting them here fails the gate inside the build while a full
# checkout passes. `erun-ui` also carries the `frontend/` Yarn-workspace
# member whose own gate `test-frontend` now runs here too (see root
# AGENTS.md's Makefile comment).
COPY erun-backend/erun-backend-api /src/erun-backend/erun-backend-api
# TestDockerfileLdflagsActuallyStampsTheBinary reads this Dockerfile at test
# time to lock its -ldflags target to the real release build, so the test
# stage needs the file even though nothing here builds the eapi image itself.
COPY erun-devops/docker/erun-backend-api/Dockerfile /src/erun-devops/docker/erun-backend-api/Dockerfile
COPY erun-ui /src/erun-ui
COPY erun-kit /src/erun-kit
COPY erun-console /src/erun-console
COPY Formula /src/Formula
COPY bucket /src/bucket
# helm-chart-tests renders every erun-devops/k8s chart with `helm template`;
# only the charts and their *_test.sh scripts are needed, not the rest of
# erun-devops (docker/, terraform-erun/, etc.).
COPY erun-devops/k8s /src/erun-devops/k8s
# erun-backend-db-chart_test.sh cross-checks the data-retention doc's claimed
# default and chart-value keys (retention.enabled, retention.dryRun) against
# what this chart actually renders; only the one page is needed.
COPY erun-docs/docs/deployment/data-retention.md /src/erun-docs/docs/deployment/data-retention.md
# TestMCPOverviewDocumentsEveryTool cross-checks erun-common's registered MCP
# tools against this page's "Full tool index" table; only the one file is
# needed, not the built docs site.
COPY erun-docs/docs/mcp/overview.md /src/erun-docs/docs/mcp/overview.md
# TestGateRunInfrastructureSignaturesAreDocumented (erun-integration) cross-
# checks erun-common's known infrastructure-failure signature list against
# this page's spec of the gate-run/gate-build failure classifier.
COPY erun-docs/docs/agent-reference/skills-spec.md /src/erun-docs/docs/agent-reference/skills-spec.md

# `make check` runs golangci-lint across the Go modules, erun-ui's Go tests,
# the frontend kit + console gates, the erun-devops/k8s chart tests, then the
# integration suite + coverage gate. A failing step fails the build, the
# builder stage's
# COPY --from=test /test-ok fails, and no image is tagged. The Yarn cache
# mount lets `yarn install` reuse fetched packages across builds the same way
# the Go module/build caches do.
#
# DIND_MEMORY_LIMIT_MIB defaults to the erun-dind sidecar's own chart default
# (runtime.dind.resources.limits.memory, "20480Mi" in
# erun-devops/k8s/erun-devops/templates/service.yaml) and feeds
# PARALLEL_GATE_MEMORY_LIMIT_MIB, which LINT_PARALLELISM/HELM_CHART_TEST_PARALLELISM
# use to size their fan-out. This RUN step executes in a cgroup that is a
# sibling of the sidecar's own limited cgroup, not a descendant of it, so
# `cat /sys/fs/cgroup/memory.max` here reads "max" regardless of what the
# sidecar is actually configured with -- scripts/parallel-gate.sh's own cgroup
# reads are structurally blind in exactly this context. Passing the sidecar's
# real ceiling in as a build arg is what lets the width calculation size
# concurrent lint/test processes against the memory the operator actually
# provisioned instead of silently dropping the memory term and sizing off CPU
# quota alone (see that script's own comment on PARALLEL_GATE_MEMORY_LIMIT_MIB).
# `erun build` itself passes this as a real --build-arg derived from the
# building environment's actual EnvConfig.RuntimeDindPod (applyDindResourceBuildArgs
# in erun-common/build_dind_resources.go), so the default below is only what a
# bare `docker build` (no erun env resolved) falls back to -- it is
# deliberately the same conservative constant the sidecar's own chart default
# and NormalizeRuntimeDindPodResources use, never the host node's capacity. A
# tenant image built from a `<tenant>-devops` Dockerfile with a different
# sidecar memory default only needs to change this fallback to match; erun's
# own resolved value already tracks the environment.
ARG DIND_MEMORY_LIMIT_MIB=20480
# DIND_CPU_LIMIT is the same blind-cgroup problem as DIND_MEMORY_LIMIT_MIB
# above, for CPU instead of memory. playwright.config.ts's own worker-count
# derivation reads os.availableParallelism() -- quota-aware everywhere else --
# but this RUN step is a sibling of the erun-dind sidecar's own limited
# cgroup, not a descendant of it, so cpu.max reads "max" here regardless of
# the sidecar's configured limit and the derivation falls through to the
# node's real core count (24 on the pod #2081 was measured on), oversized for
# an environment that does not own that many cores exclusively -- 6 workers
# (6 Go backends + 6 headless Chromiums) contending with everything else
# scheduled on the node produced a different spec failing every run (#2081).
# Default mirrors runtime.dind.resources.limits.cpu / eruncommon.DefaultRuntimeDindCPU
# ("4"), the same source DIND_MEMORY_LIMIT_MIB's default mirrors for memory --
# the CPU budget the operator actually provisioned for this environment's
# build work, rather than the node's raw core count. ERUN_PLAYWRIGHT_WORKERS
# applies the config's own "two cores per worker" rule (a worker is a Go
# backend *and* a headless Chromium, and they compete) to that budget instead
# of to the unbounded node, floored at 1 and capped at 4 -- one more than the
# config comment's own "2 workers clean on 4 cores" data point, kept as a
# ceiling rather than raised further without new measurement. Like
# DIND_MEMORY_LIMIT_MIB above, `erun build` passes this as a real --build-arg
# resolved from EnvConfig.RuntimeDindPod, so the default below is only the
# bare-`docker build` fallback -- the same constant the sidecar's own chart
# default and NormalizeRuntimeDindPodResources use, never the node's core
# count. A tenant image with a different sidecar CPU default only needs to
# change this fallback to match.
ARG DIND_CPU_LIMIT=4
# PLAYWRIGHT_TEST_AREAS carries `erun build`'s resolved smoke+area selection
# (applyPlaywrightAreaBuildArgs in erun-common/build_playwright_areas.go,
# derived from the Playwright spec-file diff against the merge base) into
# this stage's `make test-playwright` invocation below, per the design in
# erun-ui/playwright/AGENTS.md's "Area-scoped gate selection": "all" or unset
# runs the full suite, "smoke" runs only tests/smoke, "smoke,<area>,..." runs
# smoke plus the named area directories under tests/areas/. Left at its
# default empty string for a bare `docker build` with no erun build-arg
# resolution -- the safe, fail-open direction is the full suite, never zero
# coverage.
ARG PLAYWRIGHT_TEST_AREAS=""
# golangci-lint's own analysis cache sat unmounted here even though the Go
# module and build caches right above it were: HOME is /root in this stage
# (root user, no ENV HOME override), so the tool's default cache dir is
# /root/.cache/golangci-lint -- set explicitly rather than left to that
# default resolving as expected. Content-hash keyed by golangci-lint itself
# (file contents + linter/version identity), so a stale entry cannot make a
# real finding disappear; it only skips re-analyzing files that did not
# change. Measured on this exact cache (a single small module): cold ~32s,
# warm ~1.2s.
#
# The frontend lint/format cache below is the TypeScript-side equivalent:
# eslint/prettier's own --cache, mounted so it survives across builds. Both
# tools are invoked (from the Makefile's test-frontend target) with
# --cache-strategy content, not the metadata/mtime default, because every
# COPY in this Dockerfile stamps a fresh mtime on every file on every build --
# verified empirically that the metadata strategy is therefore a permanent
# cache miss under Docker, while content hashing survives it (measured:
# eslint 20s cold / 2.5s warm on one workspace after simulating a COPY's
# mtime reset). The Makefile points both tools at $(CURDIR)/.cache/
# frontend-lint, which resolves to /src/.cache/frontend-lint here (this
# stage's WORKDIR), so one mount covers all three workspaces' eslint+prettier
# caches without the Dockerfile needing to inject a path.
#
# test-playwright's own `playwright install chromium` (run.sh) had the same
# unmounted-cache shape as golangci-lint: its default download target,
# ~/.cache/ms-playwright, resolves to /root/.cache/ms-playwright here for the
# same HOME=/root reason, and a live check of that directory on a real
# environment found it holding ~640MB (chromium + chromium_headless_shell +
# ffmpeg). Playwright itself decides whether to (re)download by checking the
# already-installed revision against PLAYWRIGHT_VERSION before touching the
# network, so a warm mount is a pure skip of an unchanged, pinned-version
# download -- never a source of stale test behaviour, since the browser
# binary is never what a source change here would need to invalidate.
#
# The yarn download cache mount itself was the same shape one layer deeper:
# yarn classic has no ENV-driven default of its own, so it fell back to
# $HOME/.cache/yarn (confirmed by reading yarn's own getCacheDir() and by
# running it with HOME pointed at an owned test directory) -- /root/.cache/yarn
# in this stage, never the /usr/local/share/.cache/yarn this RUN has always
# mounted. YARN_CACHE_FOLDER (a real yarn classic config key yarn maps from
# its env-var form, confirmed the same way) redirects it to the path that is
# actually mounted, instead of moving the mount to match /root -- keeping the
# established path every other yarn-using Dockerfile in the repo already
# mounts. Measured with the real root workspace lockfile, cold vs. warm at
# this exact path: yarn install 53s cold (1.5GB fetched from the registry) vs.
# 10.7s warm. `--frozen-lockfile` resolves every package from yarn.lock
# regardless of what the cache holds, so a cache hit only skips re-downloading
# bytes already pinned by the lockfile; it can't change what gets installed.
#
# /src/.cache/frontend-vite and /src/.cache/frontend-tsbuildinfo are erun-ui's
# own incrementality: erun-ui/frontend/vite.config.ts and tsconfig.json point
# vite's transform cache and tsc's build-info file at repo-relative paths
# under these two dirs (`../../.cache/frontend-vite/erun-ui-frontend`,
# `../../.cache/frontend-tsbuildinfo/erun-ui-frontend.tsbuildinfo`), which
# resolve here since this stage's WORKDIR is /src -- no env var needed, the
# Dockerfile only has to mount the target the source config already names.
#
# ERUN_WAILSJS_CACHE_DIR is erun-ui/generate-wailsjs.sh's skip-when-unchanged
# cache (see that script's own comment): it hashes the Go source that defines
# the bound Wails API and, on a match, restores the last-generated
# frontend/wailsjs/ instead of re-running `wails generate module`. This is
# what makes the second of this build's two generations a no-op -- test-frontend
# (below) generates it once, and build.sh generates it again inside
# test-playwright's headless erun-app build -- and what survives a COPY-layer
# cache invalidation (e.g. an unrelated frontend source edit) that has
# nothing to do with the bound Go API. Unset outside this Dockerfile, so a
# bare `./generate-wailsjs.sh` still regenerates unconditionally.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    --mount=type=cache,target=/root/.cache/ms-playwright \
    --mount=type=cache,target=/usr/local/share/.cache/yarn \
    --mount=type=cache,target=/src/.cache/frontend-lint \
    --mount=type=cache,target=/src/.cache/frontend-vite \
    --mount=type=cache,target=/src/.cache/frontend-tsbuildinfo \
    --mount=type=cache,target=/root/.cache/erun-wailsjs \
    GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    ERUN_WAILSJS_CACHE_DIR=/root/.cache/erun-wailsjs \
    PARALLEL_GATE_MEMORY_LIMIT_MIB="${DIND_MEMORY_LIMIT_MIB}" \
    PARALLEL_GATE_CPU_LIMIT="${DIND_CPU_LIMIT}" \
    ERUN_PLAYWRIGHT_WORKERS="$(w=$(( ${DIND_CPU_LIMIT} / 2 )); [ "$w" -ge 1 ] || w=1; [ "$w" -le 4 ] && echo "$w" || echo 4)" \
    PLAYWRIGHT_TEST_AREAS="${PLAYWRIGHT_TEST_AREAS}" \
    make check && touch /test-ok

# Pinned to BUILDPLATFORM: every RUN below cross-compiles via explicit
# GOOS/GOARCH with CGO disabled, so the compiler itself can run natively on
# the build host instead of under qemu emulation for the target arch. The
# final image's own `go` binary comes from the separate `goroot` stage below,
# not from here, so pinning this stage does not affect its arch.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

WORKDIR /src

# Copy module manifests first so dependency downloads stay cached when only source changes.
COPY erun-cli/go.mod erun-cli/go.sum erun-cli/go.work erun-cli/go.work.sum /src/erun-cli/
COPY erun-common/go.mod /src/erun-common/go.mod
COPY erun-mcp/go.mod erun-mcp/go.sum /src/erun-mcp/
COPY erun-devops/VERSION /src/erun-devops/VERSION
COPY GOLANGCI_LINT_VERSION /src/GOLANGCI_LINT_VERSION

WORKDIR /src/erun-cli

ARG TARGETOS=linux
ARG TARGETARCH
ARG ERUN_VERSION
ARG ERUN_COMMIT=""
ARG ERUN_DATE=""
ARG GOLANGCI_LINT_VERSION

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-cli /src/erun-cli
COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp

# Gate the two heavy compiles below on the test stage instead of gating only
# the final artefact after them: test and builder are otherwise independent
# stages (same base image, no data dependency), so BuildKit is free to -- and
# does -- run them at once. That lets `make check`'s own golangci-lint pass
# race a *second* full from-source golangci-lint build (this stage's own
# install below) plus the erun/emcp compiles -- two heavy Go compilations
# sharing the fixed-memory erun-dind sidecar at the same time on a genuine
# full rebuild, which is what actually exhausts it, not concurrent
# architectures (the per-platform build loop already runs strictly one at a
# time). Moving the gate here also means a failing test no longer lets
# builder spend a full compile before discovering it.
COPY --from=test /test-ok /tmp/erun-test-ok

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ERUN_VERSION="${ERUN_VERSION:-$(tr -d '\n' < /src/erun-devops/VERSION)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/cmd.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${ERUN_DATE}" -o /out/erun . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/erun-mcp.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/erun-mcp.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/erun-mcp.buildDate=${ERUN_DATE}" -o /out/emcp ../erun-mcp/cmd/emcp

# Build golangci-lint from source against this stage's Go toolchain so
# the lint binary's embedded analyzer libraries (go/ast, go/types,
# go/parser) always match the Go version targeted by go.mod. The
# upstream prebuilt tarball is compiled with whatever Go the
# golangci-lint maintainers used at release time, which drifts behind
# erun's go.mod bumps and aborts pre-commit with "go language version
# used to build golangci-lint is lower than the targeted Go version".
#
# GOBIN is intentionally left unset: `go install` refuses to write to an
# explicit GOBIN when cross-compiling, so with the stage pinned to
# BUILDPLATFORM (TARGETARCH != host on the foreign-arch pass) the install
# instead lands under $(go env GOPATH)/bin/, in a <GOOS>_<GOARCH>/ subdir
# when cross-compiling or directly in bin/ when it matches the host.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-$(tr -d '\n' < /src/GOLANGCI_LINT_VERSION)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" && \
    mkdir -p /out && \
    find "$(go env GOPATH)/bin" -name golangci-lint -exec cp {} /out/golangci-lint \;

# Target-arch Go toolchain for the final image's own `go` binary (needed for
# in-pod contribute-mode builds, e.g. erun-ui). Deliberately not pinned to
# BUILDPLATFORM: this stage ships into the runtime image and runs no RUN
# instructions, so sourcing it costs a per-arch layer pull, not emulation.
FROM golang:1.26.0 AS goroot

FROM ghcr.io/sophium/erun-ubuntu:noble-20260217

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.33.1
# HELM_VERSION's default lives in the global ARG at the top of this file,
# shared with the test stage's own helm install.
ARG HELM_VERSION
ARG TERRAFORM_VERSION=1.12.2
ARG K9S_VERSION=v0.50.9
ARG DOCKER_VERSION=28.1.1
ARG BUILDX_VERSION=v0.18.0
ARG DIFFT_VERSION=0.68.0
ARG GH_VERSION=2.74.2
ARG RIPGREP_VERSION=15.1.0
ARG NODE_VERSION=24.14.0
ARG YARN_VERSION=1.22.22
ARG CODEX_VERSION=0.125.0
ARG CLAUDE_CODE_VERSION=2.1.222
ARG AWS_CLI_VERSION=2.32.16
# ATLAS_VERSION's default lives in the global ARG at the top of this file,
# shared with the test stage's own atlas install.
ARG ATLAS_VERSION
# Default lives in the global ARG at the top of this file, shared with the
# test stage's own install-deps call. Keep it in sync with the
# @playwright/test version resolved in erun-ui/playwright/yarn.lock so the
# installed Chromium system libraries match the browser the suite downloads
# at run time.
ARG PLAYWRIGHT_VERSION

USER root

# Keep external tooling installs pinned and centralized in one layer for traceability.
#
# build-essential + pkg-config + libgtk-3-dev + libwebkit2gtk-4.1-dev exist
# so the in-pod contribute-mode workflow can rebuild erun-ui (the Wails
# desktop binary) and run it in headless mode against a port-forward.
# Wails' Go bindings link against webkit2gtk on Linux via CGO; without
# the dev headers `go build -tags desktop,production` fails at compile.
# Cost: roughly +400 MB on the final image. Worth it because the
# contribute-app browser bridge is the only way to test desktop-side
# changes without leaving the env.
RUN apt-get update && \
    apt-get install -y git bash-completion bubblewrap dpkg-dev dtach iproute2 procps tini \
        build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev && \
    rm -rf /var/lib/apt/lists/* && \
    # ripgrep ships a musl static build for amd64 but only a gnu build for
    # arm64 — the asymmetric triples are deliberate, not a typo.
    case "${TARGETARCH}" in \
        amd64) arch=amd64; docker_arch=x86_64; node_arch=x64; aws_arch=x86_64; rg_triple=x86_64-unknown-linux-musl ;; \
        arm64) arch=arm64; docker_arch=aarch64; node_arch=arm64; aws_arch=aarch64; rg_triple=aarch64-unknown-linux-gnu ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" && \
    curl -fsSL -o /usr/local/bin/atlas "https://release.ariga.io/atlas/atlas-linux-${arch}-${ATLAS_VERSION}" && \
    curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${arch}.tar.gz" && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    install -m 0755 "/tmp/linux-${arch}/helm" /usr/local/bin/helm && \
    curl -fsSL -o /tmp/terraform.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${arch}.zip" && \
    unzip -q /tmp/terraform.zip -d /tmp && \
    install -m 0755 /tmp/terraform /usr/local/bin/terraform && \
    curl -fsSL -o /tmp/k9s.tar.gz "https://github.com/derailed/k9s/releases/download/${K9S_VERSION}/k9s_Linux_${arch}.tar.gz" && \
    tar -xzf /tmp/k9s.tar.gz -C /tmp && \
    install -m 0755 /tmp/k9s /usr/local/bin/k9s && \
    curl -fsSL -o /tmp/difft.tar.gz "https://github.com/Wilfred/difftastic/releases/download/${DIFFT_VERSION}/difft-${docker_arch}-unknown-linux-gnu.tar.gz" && \
    tar -xzf /tmp/difft.tar.gz -C /tmp && \
    install -m 0755 /tmp/difft /usr/local/bin/difft && \
    curl -fsSL -o /tmp/ripgrep.tar.gz "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-${rg_triple}.tar.gz" && \
    tar -xzf /tmp/ripgrep.tar.gz -C /tmp && \
    install -m 0755 "/tmp/ripgrep-${RIPGREP_VERSION}-${rg_triple}/rg" /usr/local/bin/rg && \
    curl -fsSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" && \
    tar -xzf /tmp/gh.tar.gz -C /tmp && \
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh && \
    curl -fsSL -o /tmp/docker.tgz "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_VERSION}.tgz" && \
    tar -xzf /tmp/docker.tgz -C /tmp && \
    install -m 0755 /tmp/docker/docker /usr/local/bin/docker && \
    curl -fsSL -o /tmp/docker-buildx "https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}/buildx-${BUILDX_VERSION}.linux-${arch}" && \
    install -D -m 0755 /tmp/docker-buildx /usr/local/lib/docker/cli-plugins/docker-buildx && \
    curl -fsSL -o /tmp/node.tar.gz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz" && \
    tar -xzf /tmp/node.tar.gz -C /usr/local --strip-components=1 && \
    curl -fsSL -o /tmp/awscliv2.zip "https://awscli.amazonaws.com/awscli-exe-linux-${aws_arch}-${AWS_CLI_VERSION}.zip" && \
    unzip -q /tmp/awscliv2.zip -d /tmp && \
    /tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli && \
    npm install -g "@openai/codex@${CODEX_VERSION}" "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" "yarn@${YARN_VERSION}" && \
    mv /usr/local/bin/codex /usr/local/bin/codex-real && \
    mv /usr/local/bin/claude /usr/local/bin/claude-real && \
    npm cache clean --force && \
    rm -rf /tmp/helm.tar.gz /tmp/terraform.zip /tmp/k9s.tar.gz /tmp/difft.tar.gz /tmp/ripgrep.tar.gz /tmp/gh.tar.gz /tmp/docker.tgz /tmp/docker-buildx /tmp/node.tar.gz /tmp/awscliv2.zip "/tmp/linux-${arch}" "/tmp/gh_${GH_VERSION}_linux_${arch}" "/tmp/ripgrep-${RIPGREP_VERSION}-${rg_triple}" /tmp/terraform /tmp/k9s /tmp/difft /tmp/docker /tmp/aws && \
    groupmod -n erun ubuntu && \
    usermod -l erun -d /home/erun -m -g erun ubuntu && \
    passwd -d erun && \
    mkdir -p /home/erun/git /home/erun/.kube /home/erun/.config/k9s /home/erun/.erun/outputs /etc/erun /etc/bash_completion.d /opt/erun && \
    printf '\n[ -r /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion\n[ -r /etc/bash_completion.d/erun ] && . /etc/bash_completion.d/erun\n[ -r /etc/erun/bash_prompt.sh ] && . /etc/erun/bash_prompt.sh\n' >> /etc/bash.bashrc && \
    chown -R erun:erun /home/erun && \
    chmod 0755 /usr/local/bin/kubectl /usr/local/bin/atlas /usr/local/bin/helm /usr/local/bin/terraform /usr/local/bin/k9s /usr/local/bin/difft /usr/local/bin/rg /usr/local/bin/docker /usr/local/bin/aws

# Chromium runtime libraries for the erun-ui Playwright suite. The build deps
# in the layer above only cover compiling erun-app (Wails links webkit2gtk via
# CGO); the headless browser that `erun-ui/playwright/run.sh` drives needs its
# own shared libraries (libnss3, libnspr4, and the GTK/X/ALSA runtime set).
# Without them, in-pod contribute-mode can build erun-ui but cannot run its
# mandatory UI tests. Use Playwright's own dependency installer, pinned to the
# suite's version, so the package set stays correct across Ubuntu library
# renames (the 24.04 `t64` transition) instead of hand-maintaining a fragile
# list. The browser binary itself is installed without root by run.sh's
# `playwright install chromium`; only these system libs need root, so they
# belong in the image. Kept as a separate layer (not folded into the tooling
# layer above) because install-deps runs its own apt-get update after that
# layer has already cleared the apt lists.
RUN npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium && \
    rm -rf /var/lib/apt/lists/* /root/.npm

COPY --from=goroot /usr/local/go /usr/local/go
COPY --from=builder --chmod=0755 /out/erun /usr/local/bin/erun
COPY --from=builder --chmod=0755 /out/emcp /usr/local/bin/emcp
COPY --from=builder --chmod=0755 /out/golangci-lint /usr/local/bin/golangci-lint
COPY --chmod=0755 erun-devops/docker/erun-devops/entrypoint.sh /usr/local/bin/erun-devops-entrypoint
COPY --chmod=0755 erun-devops/docker/erun-devops/skills-install.sh /usr/local/bin/erun-install-skills
COPY --chmod=0755 erun-devops/docker/erun-devops/agents-install.sh /usr/local/bin/erun-install-agents
# erun-link-release symlinks a sourceless runtime env's git folder at
# /opt/erun/release so a tenant devops image can surface baked artifacts there
# (outside the /home/erun PVC, which would otherwise shadow them).
COPY --chmod=0755 erun-devops/docker/erun-devops/release-link.sh /usr/local/bin/erun-link-release
# erun-prune-sessions clears persistent desktop session sockets left with no
# dtach server behind them, so a replaced pod never presents a leftover socket
# as a running session.
COPY --chmod=0755 erun-devops/docker/erun-devops/session-prune.sh /usr/local/bin/erun-prune-sessions
# erun-adopt-worktree runs in the chart's init container, where the home volume
# and the worktree claim are staged side by side, so an environment whose
# checkout predates the claim is moved onto it instead of being masked by it.
COPY --chmod=0755 erun-devops/docker/erun-devops/worktree-adopt.sh /usr/local/bin/erun-adopt-worktree
# erun-oidc-bootstrap runs as the erun-zitadel chart's oidc-bootstrap sidecar,
# reconciling the platform's OIDC applications against the Management API.
COPY --chmod=0755 erun-devops/docker/erun-devops/oidc-bootstrap.sh /usr/local/bin/erun-oidc-bootstrap
COPY --chmod=0755 erun-devops/docker/erun-devops/codex-wrapper.sh /usr/local/bin/codex
COPY --chmod=0755 erun-devops/docker/erun-devops/claude-wrapper.sh /usr/local/bin/claude
COPY --chmod=0644 erun-devops/docker/erun-devops/bash_prompt.sh /etc/erun/bash_prompt.sh
# Directories need the execute/search bit so the unprivileged erun user can
# enter them; the entrypoint copies each skill into ~/.claude/skills and
# ~/.codex/skills (re-chmodding the destination files to 0644). 0644 here would
# leave the dirs un-searchable and the install would silently copy nothing.
COPY --chmod=0755 erun-skills/skills /etc/erun/skills
# Reusable agents are flat <name>.md files (no subdirectory to need the
# execute bit for), so 0644 is enough here.
COPY --chmod=0644 erun-skills/agents /etc/erun/agents

RUN /usr/local/bin/erun completion bash > /etc/bash_completion.d/erun && \
    chmod 0644 /etc/bash_completion.d/erun

ENV HOME=/home/erun
ENV PATH=/usr/local/go/bin:${PATH}
ENV GOPATH=/home/erun/go
ENV KUBECONFIG=/home/erun/.kube/config
ENV DOCKER_HOST=unix:///var/run/docker.sock
ENV DOCKER_BUILDKIT=1
WORKDIR /home/erun

USER erun

EXPOSE 17000 17033 17022 9100

# Run PID 1 under tini so orphaned children are reaped. The entrypoint daemonises
# sshd, whose per-connection session children reparent to PID 1 when they finish;
# with workspace-sync opening an SSH connection every couple of seconds, an
# unreaped PID 1 accumulates thousands of <defunct> sshd zombies until the node's
# process table is exhausted and no container can fork (cluster-wide crashloop).
ENTRYPOINT ["/usr/bin/tini", "--", "erun-devops-entrypoint"]
CMD ["devops"]
