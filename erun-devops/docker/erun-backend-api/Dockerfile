ARG ERUN_VERSION=1.0.0

# `dist` builds FROM `test`, so no image exists unless the tests passed.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build

WORKDIR /src

# go.mod replaces erun-common, so its go.mod is needed to resolve requires.
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

# Database suites start PostgreSQL via Testcontainers at DOCKER_HOST, hence --network=host.
FROM build AS test

COPY erun-backend/erun-backend-db /src/erun-backend/erun-backend-db
# buildinfo_ldflags_test.go reads this Dockerfile.
COPY erun-devops/docker/erun-backend-api /src/erun-devops/docker/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    go mod tidy -diff && go tool golangci-lint run ./... && go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path .

ARG DOCKER_HOST=tcp://127.0.0.1:2375
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --network=host \
    go test -count=1 ./...

# -X on a package-main symbol only works as "main.<name>"; the full import path is silently ignored.
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

# eapi defaults to loopback; a container must bind all interfaces.
ENV ERUN_API_HOST=0.0.0.0

EXPOSE 17033

ENTRYPOINT ["eapi"]
