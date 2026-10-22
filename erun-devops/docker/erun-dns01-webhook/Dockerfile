ARG ERUN_VERSION=1.0.0

# The module's own lint and tests are this image's gate: the builder depends on
# this stage through `COPY --from=test`, so no image is produced when they fail.
# The linter is pinned here rather than as a go.mod tool: as a tool its
# dependency graph would force the webhook's cert-manager and k8s.io versions
# up, and a lint pin must not change what ships.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

WORKDIR /src/erun-devops/dns01-webhook

COPY erun-devops/dns01-webhook/go.mod erun-devops/dns01-webhook/go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-devops/dns01-webhook ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.2.2 run ./... && \
    go test -count=1 ./... && \
    touch /test-ok

# Pinned to BUILDPLATFORM: the RUN below cross-compiles via explicit
# GOOS/GOARCH with CGO disabled, so the compiler runs natively on the build
# host instead of under qemu emulation for the target arch. Only the
# resulting binary is copied into the final image, so the stage's own
# platform does not affect the shipped artifact's arch.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

COPY --from=test /test-ok /tmp/erun-test-ok

WORKDIR /src/erun-devops/dns01-webhook

COPY erun-devops/dns01-webhook/go.mod erun-devops/dns01-webhook/go.sum ./

ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-devops/dns01-webhook ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/dns01-webhook .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=builder --chmod=0755 /out/dns01-webhook /usr/local/bin/dns01-webhook

USER erun

# The cert-manager aggregated apiserver serves HTTPS on 443.
EXPOSE 443

ENTRYPOINT ["dns01-webhook"]
