ARG ERUN_VERSION=1.0.0

FROM golang:1.26.0 AS builder

WORKDIR /src

COPY erun-backend/erun-backend-api/go.mod erun-backend/erun-backend-api/go.sum /src/erun-backend/erun-backend-api/

WORKDIR /src/erun-backend/erun-backend-api

ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

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
