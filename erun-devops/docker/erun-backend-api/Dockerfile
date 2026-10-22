ARG ERUN_VERSION=1.0.0

# The component's own tests are this image's gate: the builder below depends on
# this stage through `COPY --from=test`, so no image is produced when they fail.
# The stage only calls the module's ordinary `go test ./...`. Its database
# suites start PostgreSQL themselves through Testcontainers, against the Docker
# daemon `erun build` provides at DOCKER_HOST -- reachable from a
# `RUN --network=host` step, which `erun build` grants a Dockerfile with a
# `test` stage.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

WORKDIR /src

COPY erun-backend/erun-backend-api/go.mod erun-backend/erun-backend-api/go.sum /src/erun-backend/erun-backend-api/
COPY erun-common/go.mod /src/erun-common/go.mod

WORKDIR /src/erun-backend/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-common /src/erun-common
COPY erun-backend/erun-backend-api /src/erun-backend/erun-backend-api
COPY erun-backend/erun-backend-db /src/erun-backend/erun-backend-db
# cmd/eapi/buildinfo_ldflags_test.go's TestDockerfileLdflagsActuallyStampsTheBinary
# opens ../../../../erun-devops/docker/erun-backend-api/Dockerfile at run time
# to prove the release ldflags line still reaches the compiled binary.
COPY erun-devops/docker/erun-backend-api /src/erun-devops/docker/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --network=host DOCKER_HOST=tcp://127.0.0.1:2375 \
    go test -count=1 ./... && touch /test-ok

# Pinned to BUILDPLATFORM: the RUN below cross-compiles via explicit
# GOOS/GOARCH with CGO disabled, so the compiler runs natively on the build
# host instead of under qemu emulation for the target arch. Only the
# resulting binary is copied into the final image, so the stage's own
# platform does not affect the shipped artifact's arch.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

# The marker the `test` stage above leaves behind, copied before the builder's
# own work so a failed gate stops the build here rather than after compiling a
# binary nothing will ship. This instruction is also what makes the test stage a
# required part of the build graph: an image built from this Dockerfile has
# always passed its tests, so a fingerprint promotion or a BuildKit cache hit
# reuses a tested result.
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

USER erun
WORKDIR /home/erun

# eapi reads its configuration from the environment the chart sets; only the
# bind address differs from its loopback default inside a container.
ENV ERUN_API_HOST=0.0.0.0

EXPOSE 17033

ENTRYPOINT ["eapi"]
