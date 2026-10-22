# erun-cli builds and tests the erun CLI and the emcp server: the `test` stage
# only calls each Go module's own lint and tests, and the builder copies its
# marker, so no binary exists unless they passed. The image is FROM scratch and
# holds just /erun and /emcp; the erun-devops image copies them from it.
#
# erun-common is the shared library both binaries link, so it is tested here by
# its consumer; erun-integration is the CLI's compiled-binary suite.

# Pinned to BUILDPLATFORM so the suite runs natively once per build instead of
# once per target arch under emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

# dtach for erun-mcp's attach tests, which run a real `dtach -A` session.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    apt-get update && apt-get install -y --no-install-recommends dtach

# atlas for the integration suite's gate-merge/merge atlas.sum scenarios, which
# run the real `atlas migrate hash` rather than a stub of its hashing.
ARG BUILDARCH
ARG ATLAS_VERSION=v1.2.0
RUN curl -fsSL -o /usr/local/bin/atlas "https://release.ariga.io/atlas/atlas-linux-${BUILDARCH}-${ATLAS_VERSION}" && \
    chmod 0755 /usr/local/bin/atlas

WORKDIR /src

# Module manifests first so dependency downloads stay cached when only source
# changes.
COPY erun-common/go.mod erun-common/go.sum /src/erun-common/
COPY erun-mcp/go.mod erun-mcp/go.sum /src/erun-mcp/
COPY erun-cli/go.mod erun-cli/go.sum erun-cli/go.work erun-cli/go.work.sum /src/erun-cli/
COPY erun-integration/go.mod erun-integration/go.work erun-integration/go.work.sum /src/erun-integration/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    for m in erun-common erun-mcp erun-cli erun-integration; do (cd "$m" && go mod download) || exit 1; done

COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp
COPY erun-cli /src/erun-cli
COPY erun-integration /src/erun-integration

# What the suites read from the rest of the checkout: the build-context and
# project config they check, the Dockerfiles and charts erun builds and
# deploys, the API routes and desktop/console sources the surface audits
# enumerate, and the docs and skill pages the drift checks compare against.
COPY .dockerignore .gitignore /src/
COPY .erun/config.yaml /src/.erun/config.yaml
COPY erun-devops/VERSION /src/erun-devops/VERSION
COPY erun-devops/docker /src/erun-devops/docker
COPY erun-devops/k8s /src/erun-devops/k8s
COPY erun-devops/terraform-erun /src/erun-devops/terraform-erun
COPY erun-backend/erun-backend-api/internal/routes /src/erun-backend/erun-backend-api/internal/routes
COPY erun-backend/erun-backend-api/internal/routeroles /src/erun-backend/erun-backend-api/internal/routeroles
COPY erun-ui /src/erun-ui
COPY erun-console/src /src/erun-console/src
COPY erun-docs/docs /src/erun-docs/docs
COPY erun-skills /src/erun-skills

ENV GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-common && go tool golangci-lint run ./... && go test -race -count=1 ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-mcp && go tool golangci-lint run ./... && go test -count=1 ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-cli && go tool golangci-lint run ./... && go test -count=1 ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-integration && go tool golangci-lint run ./... && go test -count=1 -timeout=45m ./... && \
    touch /test-ok

# Pinned to BUILDPLATFORM: the binaries are cross-compiled with explicit
# GOOS/GOARCH and CGO disabled, so the compiler runs natively on the build host.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

# The test stage's marker, copied before any compile: it makes the tests a
# required part of the build graph and stops a failed gate before compiling.
COPY --from=test /test-ok /tmp/erun-test-ok

WORKDIR /src

COPY erun-cli/go.mod erun-cli/go.sum erun-cli/go.work erun-cli/go.work.sum /src/erun-cli/
COPY erun-common/go.mod erun-common/go.sum /src/erun-common/
COPY erun-mcp/go.mod erun-mcp/go.sum /src/erun-mcp/
COPY erun-devops/VERSION /src/erun-devops/VERSION

WORKDIR /src/erun-cli

ARG TARGETOS=linux
ARG TARGETARCH
ARG ERUN_COMMIT=""
ARG ERUN_DATE=""

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-cli /src/erun-cli
COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp

ARG ERUN_VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ERUN_VERSION="${ERUN_VERSION:-$(tr -d '\n' < /src/erun-devops/VERSION)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/cmd.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${ERUN_DATE}" -o /out/erun . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/erun-mcp.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/erun-mcp.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/erun-mcp.buildDate=${ERUN_DATE}" -o /out/emcp ../erun-mcp/cmd/emcp

FROM scratch

COPY --from=builder /out/erun /erun
COPY --from=builder /out/emcp /emcp
