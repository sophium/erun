ARG ERUN_VERSION=1.0.0

# Stages: `build` compiles the module natively, `test` runs its own lint and
# tests on top of that, `dist` (FROM test) stamps the version and
# cross-compiles for the target, and `runtime` ships the binary from `dist`.
# Because the shipped binary comes through `test`, no image exists unless the
# tests passed, and a version bump or a second architecture reruns only `dist`.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build

WORKDIR /src

# erun-backend-api's go.mod has `replace … => ../../erun-common`, so the
# replace target's go.mod is needed to resolve its requires.
COPY erun-backend/erun-backend-api/go.mod erun-backend/erun-backend-api/go.sum /src/erun-backend/erun-backend-api/
COPY erun-common/go.mod /src/erun-common/go.mod

WORKDIR /src/erun-backend/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-common /src/erun-common
COPY erun-backend/erun-backend-api /src/erun-backend/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/eapi ./cmd/eapi

# The database suites start PostgreSQL themselves through Testcontainers,
# against the daemon at DOCKER_HOST, reachable from a `RUN --network=host` step
# (`erun build` grants that entitlement to a Dockerfile with a `test` stage).
FROM build AS test

COPY erun-backend/erun-backend-db /src/erun-backend/erun-backend-db
# cmd/eapi/buildinfo_ldflags_test.go reads this Dockerfile to prove the `dist`
# stage's ldflags line still reaches the compiled binary.
COPY erun-devops/docker/erun-backend-api /src/erun-devops/docker/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    go tool golangci-lint run ./...

ARG DOCKER_HOST=tcp://127.0.0.1:2375
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --network=host \
    go test -count=1 ./...

# Cross-compiles with CGO disabled, so the compiler runs natively on the build
# host. buildVersion lives in package main (cmd/eapi), and the linker only
# honors -X against a package-main symbol written as "main.<name>": the full
# import path links with no warning and leaves the variable at its default.
FROM test AS dist

ARG TARGETOS=linux
ARG TARGETARCH
ARG ERUN_VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.buildVersion=${ERUN_VERSION}" -o /out/eapi ./cmd/eapi

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /home/erun && \
    chown -R erun:erun /home/erun

COPY --from=dist --chmod=0755 /out/eapi /usr/local/bin/eapi

USER erun
WORKDIR /home/erun

# eapi reads its configuration from the environment the chart sets; only the
# bind address differs from its loopback default inside a container.
ENV ERUN_API_HOST=0.0.0.0

EXPOSE 17033

ENTRYPOINT ["eapi"]
