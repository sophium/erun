ARG ERUN_VERSION=1.0.0

# `dist` builds FROM `test`, so no image exists unless the tests passed.
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS build

WORKDIR /src/erun-devops/dns01-webhook

COPY erun-devops/dns01-webhook/go.mod erun-devops/dns01-webhook/go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-devops/dns01-webhook ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/dns01-webhook .

# Not a go.mod tool: its dependency graph would bump the shipped cert-manager and k8s.io versions.
FROM build AS test

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    go mod tidy -diff && \
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.2.2 run ./... && \
    go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path . && \
    go test -count=1 ./...

FROM test AS dist

ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/dns01-webhook .

FROM alpine:3.24 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=dist --chmod=0755 /out/dns01-webhook /usr/local/bin/dns01-webhook

USER erun

EXPOSE 443

ENTRYPOINT ["dns01-webhook"]
