# Stage 1: builder — provisions the toolchain, runs the tests, produces the
# artefact. Example shown is Go; swap the base image, WORKDIR contents, and
# RUN commands for another toolchain (node, python, java, ...) — the shape
# (toolchain -> test -> build) stays the same regardless of language, per
# erun's multi-stage Dockerfile convention.
#
# Every COPY source below is relative to the REPO ROOT, not to this file's
# directory: erun always builds with the repo root as the context. A bare
# `COPY go.mod ./` therefore resolves at the repo root, and fails with
# "not found" when the file actually sits beside this Dockerfile.
#
# The layer order is a build-speed contract, not house style. Rarely-changing
# inputs are copied and run before frequently-changing ones, so an ordinary
# source edit reuses every layer above the source COPY.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

WORKDIR /src

# Dependency manifests alone, before the source. This layer's cache key is
# the manifests, so it survives every source-only change — which is nearly
# all of them. Copying the whole tree first would re-resolve dependencies on
# every commit instead.
COPY __SOURCE_DIR__/go.mod __SOURCE_DIR__/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Source last: the first input that differs between two commits belongs as far
# down the file as it can go.
COPY __SOURCE_DIR__/ ./

# Tests that don't depend on a deployed artefact run here. A failure fails the
# build, so no image is produced from a red test run.
#
# Deliberately above the TARGET* ARGs: nothing in this layer is
# architecture-specific, so a multi-arch build reuses one cached test layer for
# every architecture instead of re-running the suite once per platform.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...

# Everything below is per-architecture. An ARG invalidates the layers that use
# it, so these are declared as late as they can be.
ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/__COMPONENT__ .

# Stage 2: runtime — thin, no build tools, non-root. It depends on the builder
# only through the final COPY, so BuildKit provisions this stage in parallel
# with the build above rather than after it.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /home/erun && \
    chown -R erun:erun /home/erun

COPY --from=builder --chmod=0755 /out/__COMPONENT__ /usr/local/bin/__COMPONENT__

USER erun
WORKDIR /home/erun

EXPOSE __PORT__

ENTRYPOINT ["__COMPONENT__"]
