ARG ERUN_VERSION=1.0.0

# ATLAS_VERSION mirrors erun-backend-db/Dockerfile's own default and
# erun-devops/Dockerfile's top-of-file ARG: the `test` stage below applies
# erun-backend-db's real migrations, so the migrating CLI has to be the same
# Atlas the image that runs those migrations in production ships. DOCKER_VERSION
# mirrors erun-devops/Dockerfile's own default, for the same reason applied to
# the daemon client the `test` stage drives its PostgreSQL fixture with.
ARG ATLAS_VERSION=v1.2.0
ARG DOCKER_VERSION=28.1.1

# The two binaries the `test` stage below drives its PostgreSQL fixture with,
# in a stage of their own so the pinned downloads are not re-fetched on every
# build: `erun build --gate` invalidates the `test` stage's layer cache by name
# (dockerBuildGateStageArgs' --no-cache-filter), and a copy out of this stage
# survives that while a download inside it would not. Both binaries are static,
# so an Alpine downloader is a fine source for a Debian-based consumer.
FROM alpine:3.20 AS test-toolchain

ARG BUILDARCH
ARG ATLAS_VERSION
ARG DOCKER_VERSION

RUN apk add --no-cache ca-certificates curl && \
    case "${BUILDARCH}" in \
        amd64) atlas_arch=amd64; docker_arch=x86_64 ;; \
        arm64) atlas_arch=arm64; docker_arch=aarch64 ;; \
        *) echo "unsupported BUILDARCH: ${BUILDARCH}" >&2; exit 1 ;; \
    esac && \
    mkdir -p /out && \
    curl -fsSL -o /out/atlas "https://release.ariga.io/atlas/atlas-linux-${atlas_arch}-${ATLAS_VERSION}" && \
    curl -fsSL -o /tmp/docker.tgz "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_VERSION}.tgz" && \
    tar -xzf /tmp/docker.tgz -C /tmp && \
    cp /tmp/docker/docker /out/docker && \
    chmod 0755 /out/atlas /out/docker && \
    rm -rf /tmp/docker.tgz /tmp/docker

# This component's own gate venue. erun-devops/AGENTS.md's "Build Workflow"
# section is the contract it implements: an opt-in ERUN_E2E_* suite that needs
# a real PostgreSQL starts one from a `RUN --network=host` step, through the
# daemon erun's builder already runs in (DOCKER_HOST), because a `docker build`
# RUN step has no nested daemon of its own. `erun build` grants the
# `network.host` entitlement to precisely the build whose Dockerfile declares a
# `test` stage (dockerBuildEntitlementArgs), and the `COPY --from=test /test-ok`
# in the builder stage below is what makes this stage a required part of the
# build graph, so the image cannot be produced -- and therefore cannot be
# published or deployed -- when this stage failed.
#
# Before this stage existed the module's database-backed suites were inert
# everywhere: the only gate that reached them was the root Makefile's bare
# `go test -count=1 ./...`, no ERUN_E2E_* variable was set for it, and every one
# of those cases reported SKIP under a package line that still read `ok`.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

COPY --from=test-toolchain /out/atlas /usr/local/bin/atlas
COPY --from=test-toolchain /out/docker /usr/local/bin/docker

WORKDIR /src

COPY erun-backend/erun-backend-api/go.mod erun-backend/erun-backend-api/go.sum /src/erun-backend/erun-backend-api/
# The script reads erun-backend-db's migrations through the same repository
# layout the checkout has, so the stage mirrors that layout under /src rather
# than flattening it.
COPY erun-common/go.mod /src/erun-common/go.mod

WORKDIR /src/erun-backend/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-common /src/erun-common
COPY erun-backend/erun-backend-api /src/erun-backend/erun-backend-api
COPY erun-backend/erun-backend-db /src/erun-backend/erun-backend-db
# The whole component directory, not just e2e_gate_test.sh: this module's own
# tests read repository paths relative to their package directory, and
# cmd/eapi/buildinfo_ldflags_test.go's TestDockerfileLdflagsActuallyStampsTheBinary
# opens ../../../../erun-devops/docker/erun-backend-api/Dockerfile at run time
# to prove the release ldflags line still reaches the compiled binary. A stage
# that copies only the script passes in a checkout and fails inside every
# build.
COPY erun-devops/docker/erun-backend-api /src/erun-devops/docker/erun-backend-api

# 2375 is the pod-local dind listener `erun build` grants this step access to;
# it is exactly the endpoint erun-devops/AGENTS.md records as the one a test
# stage reaches. The step fails loudly if it is not there, rather than
# degrading to a run whose database-backed cases quietly skip again.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --network=host DOCKER_HOST=tcp://127.0.0.1:2375 \
    sh /src/erun-devops/docker/erun-backend-api/e2e_gate_test.sh && touch /test-ok

# Pinned to BUILDPLATFORM: the RUN below cross-compiles via explicit
# GOOS/GOARCH with CGO disabled, so the compiler runs natively on the build
# host instead of under qemu emulation for the target arch. Only the
# resulting binary is copied into the final image, so the stage's own
# platform does not affect the shipped artifact's arch.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

# The marker the `test` stage above leaves behind, copied before the builder's
# own work so a failed gate stops the build here rather than after compiling a
# binary nothing will ship. This instruction is also what makes the test stage a
# required part of the build graph: it is the `COPY --from=test` that
# dockerfileHasGateTestStage reads, so erun refuses to promote a cached
# fingerprint image for this Dockerfile and `erun build --gate` executes the
# stage instead of accepting BuildKit's replay of it.
COPY --from=test /test-ok /tmp/erun-test-ok

WORKDIR /src

COPY erun-backend/erun-backend-api/go.mod erun-backend/erun-backend-api/go.sum /src/erun-backend/erun-backend-api/
# erun-backend-api's go.mod has `replace … => ../../erun-common`, so the replace
# target must be in the build context. go mod download needs erun-common's go.mod
# to resolve its (transitive) requires; the full source is copied before go build.
COPY erun-common/go.mod /src/erun-common/go.mod

WORKDIR /src/erun-backend/erun-backend-api

ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-common /src/erun-common
COPY erun-backend/erun-backend-api /src/erun-backend/erun-backend-api

# Bakes the serving build's version into the binary (GET /v1/platform reports
# it) the same way erun/emcp do, so a client can tell whether a released fix
# is actually deployed without a write. buildVersion lives in package main
# (cmd/eapi), and the Go linker only honors -X against a package-main symbol
# when the target is written as "main.<name>" — the full package import path
# compiles and links with no warning but silently leaves the variable at its
# default.
ARG ERUN_VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.buildVersion=${ERUN_VERSION}" -o /out/eapi ./cmd/eapi

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /home/erun && \
    chown -R erun:erun /home/erun

COPY --from=builder --chmod=0755 /out/eapi /usr/local/bin/eapi
COPY --chmod=0755 erun-devops/docker/erun-backend-api/entrypoint.sh /usr/local/bin/erun-backend-api-entrypoint

USER erun
WORKDIR /home/erun

EXPOSE 17033

ENTRYPOINT ["erun-backend-api-entrypoint"]
