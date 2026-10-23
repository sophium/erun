# Stages: `build` compiles the component for the build platform, `test` runs
# its own lint and tests on top of that, `dist` (FROM test) compiles the
# shipped binary for the target architecture, and `runtime` ships it from
# `dist`. Because the shipped binary comes through `test`, BuildKit cannot skip
# the tests: no image exists unless they passed, under `erun build` and a plain
# `docker build` alike. Example shown is Go; swap the base image and RUN
# commands for another toolchain (node, python, java, ...) and keep the chain.
#
# Every COPY source below is relative to the REPO ROOT, not to this file's
# directory: erun always builds with the repo root as the context. A bare
# `COPY go.mod ./` therefore resolves at the repo root, and fails with
# "not found" when the file actually sits beside this Dockerfile.
#
# The layer order is a build-speed contract, not house style. Rarely-changing
# inputs are copied and run before frequently-changing ones, so an ordinary
# source edit reuses every layer above the source COPY.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build

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

# A native compile with no version stamp and no target-arch args, so the
# tests below build on a result that a version bump never invalidates.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build ./...

# The component's own checks. Tests that need a deployed environment do not
# belong here; they run after deploy as an e2e suite.
FROM build AS test

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test ./...

# Everything below is per-architecture. Reading TARGETARCH only here keeps the
# tested layers above identical for every build of the same source.
FROM test AS dist

ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/__COMPONENT__ .

# Thin, no build tools, non-root. It depends on the stages above only through
# the final COPY, so BuildKit provisions this stage in parallel with them.
FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /home/erun && \
    chown -R erun:erun /home/erun

COPY --from=dist --chmod=0755 /out/__COMPONENT__ /usr/local/bin/__COMPONENT__

USER erun
WORKDIR /home/erun

EXPOSE __PORT__

ENTRYPOINT ["__COMPONENT__"]
