# `runtime` ships through `test`, so no image exists unless tests pass. Swap the
# Go toolchain for another and keep the chain.
# COPY sources are relative to the repo root: erun always builds with it as context.
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS build

WORKDIR /src

# Manifests before source so dependency resolution survives source-only changes.
COPY __SOURCE_DIR__/go.mod __SOURCE_DIR__/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY __SOURCE_DIR__/ ./

# No version stamp or target-arch args, so a version bump never invalidates the tests.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build ./...

# Tests needing a deployed environment run after deploy as e2e, not here.
FROM build AS test

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test ./...

# Reading TARGETARCH only here keeps the tested layers arch-independent.
FROM test AS dist

ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/__COMPONENT__ .

# Depends on the stages above only through the final COPY, so it builds in parallel.
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
