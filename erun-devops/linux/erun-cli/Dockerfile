# The `erun` Debian package: the erun CLI with the emcp and eapi executables.
# A package component ships a .deb, never an image:
#
#   docker build -f erun-devops/linux/erun-cli/Dockerfile --target dist \
#     --platform linux/amd64 --build-arg ERUN_VERSION=<version> \
#     --output type=local,dest=<dir> .
#
# leaves erun_<version>_<arch>.deb in <dir>. There is no test stage: the CLI
# and the MCP server are linted and tested in the erun-devops image's
# `cli-test`, and the API in the erun-backend-api image's `test`.

# Pinned to BUILDPLATFORM: everything is compiled with CGO disabled, so the
# compiler runs natively and cross-compiles for the target in `package`.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build

WORKDIR /src

# Module manifests first so dependency downloads stay cached when only source
# changes.
COPY erun-common/go.mod erun-common/go.sum /src/erun-common/
COPY erun-mcp/go.mod erun-mcp/go.sum /src/erun-mcp/
COPY erun-cli/go.mod erun-cli/go.sum erun-cli/go.work erun-cli/go.work.sum /src/erun-cli/
COPY erun-backend/erun-backend-api/go.mod erun-backend/erun-backend-api/go.sum /src/erun-backend/erun-backend-api/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go -C erun-common mod download && \
    go -C erun-mcp mod download && \
    go -C erun-cli mod download && \
    go -C erun-backend/erun-backend-api mod download

COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp
COPY erun-cli /src/erun-cli
COPY erun-backend/erun-backend-api /src/erun-backend/erun-backend-api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go -C erun-cli build -o /out/erun . && \
    CGO_ENABLED=0 go -C erun-mcp build -o /out/emcp ./cmd/emcp && \
    CGO_ENABLED=0 go -C erun-backend/erun-backend-api build -o /out/eapi ./cmd/eapi

# Stamps the version, compiles for the target architecture and packs the .deb
# with erun-common's debpkg. An empty ERUN_VERSION (a bare build) takes
# erun-devops/VERSION.
FROM build AS package

COPY LICENSE /src/LICENSE
COPY erun-devops/VERSION /src/erun-devops/VERSION
ARG TARGETARCH
ARG ERUN_VERSION
ARG ERUN_DEB_MAINTAINER="ERun <erun@local>"
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go -C /src/erun-common run ./cmd/debpkg build \
        --name erun --version "${ERUN_VERSION}" --version-file /src/erun-devops/VERSION \
        --arch "${TARGETARCH}" --maintainer "${ERUN_DEB_MAINTAINER}" \
        --section utils --homepage https://github.com/sophium/erun \
        --description "Multi-tenant multi-environment deployment and management tool" \
        --description-line "ERun ships the erun CLI together with the emcp and eapi executables." \
        --go-build /src/erun-cli:.:/usr/bin/erun:github.com/sophium/erun/cmd/shared.buildVersion \
        --go-build /src/erun-mcp:./cmd/emcp:/usr/bin/emcp:github.com/sophium/erun/erun-mcp.buildVersion \
        --go-build /src/erun-backend/erun-backend-api:./cmd/eapi:/usr/bin/eapi \
        --file /src/LICENSE:/usr/share/doc/erun/copyright \
        --out /deb

# Only the .deb, so a local output export writes the package and nothing else.
FROM scratch AS dist

COPY --from=package /deb/ /
