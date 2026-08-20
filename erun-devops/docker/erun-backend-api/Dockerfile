ARG ERUN_VERSION=1.0.0

# Pinned to BUILDPLATFORM: the RUN below cross-compiles via explicit
# GOOS/GOARCH with CGO disabled, so the compiler runs natively on the build
# host instead of under qemu emulation for the target arch. Only the
# resulting binary is copied into the final image, so the stage's own
# platform does not affect the shipped artifact's arch.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

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

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/eapi ./cmd/eapi

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
