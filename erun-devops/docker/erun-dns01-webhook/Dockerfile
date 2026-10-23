ARG ERUN_VERSION=1.0.0

# Stages: `build` compiles the module natively, `test` runs its own lint and
# tests on top of that, `dist` (FROM test) cross-compiles for the target, and
# `runtime` ships the binary from `dist`. Because the shipped binary comes
# through `test`, no image exists unless the tests passed, and a second
# architecture reruns only `dist`.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build

WORKDIR /src/erun-devops/dns01-webhook

COPY erun-devops/dns01-webhook/go.mod erun-devops/dns01-webhook/go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-devops/dns01-webhook ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/dns01-webhook .

# The linter is pinned here rather than as a go.mod tool: as a tool its
# dependency graph would force the webhook's cert-manager and k8s.io versions
# up, and a lint pin must not change what ships.
FROM build AS test

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.2.2 run ./... && \
    go test -count=1 ./...

# Cross-compiles with CGO disabled, so the compiler runs natively on the build
# host.
FROM test AS dist

ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/dns01-webhook .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=dist --chmod=0755 /out/dns01-webhook /usr/local/bin/dns01-webhook

USER erun

# The cert-manager aggregated apiserver serves HTTPS on 443.
EXPOSE 443

ENTRYPOINT ["dns01-webhook"]
