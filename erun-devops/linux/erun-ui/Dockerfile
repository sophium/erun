# Ships a .deb, not an image. `package` builds FROM `test`, so no package
# exists unless the tests passed. erun-app links webkit through CGO, so a
# foreign target is cross-compiled against Debian multiarch libraries.

# Must match erun-ui/playwright's pin so install-deps matches its Chromium.
ARG PLAYWRIGHT_VERSION=1.63.0

FROM --platform=$BUILDPLATFORM node:22.14-bookworm AS node

FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    apt-get update && apt-get install -y --no-install-recommends \
        build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx && \
    npm install -g yarn@1.22.22

# Below the source copies so the layer stays cached.
ARG PLAYWRIGHT_VERSION
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium

WORKDIR /src

COPY package.json yarn.lock /src/
# The root workspace lists erun-console; its manifest keeps the frozen lockfile valid.
COPY erun-console/package.json /src/erun-console/package.json
COPY erun-kit /src/erun-kit
COPY erun-common /src/erun-common
COPY erun-ui /src/erun-ui

ENV YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn

# The bindings generator compiles the package, whose assets embed frontend/dist.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/usr/local/share/.cache/yarn \
    mkdir -p erun-ui/frontend/dist && \
    go -C erun-ui tool wails generate module && \
    yarn install --frozen-lockfile --prefer-offline --network-timeout 600000 && \
    yarn --cwd erun-ui/frontend build && \
    go -C erun-ui build -tags "desktop,production,webkit2_41" -o bin/erun-app .

FROM build AS test

# Read by buildstamp_test.go and the orchestrator tests.
COPY erun-skills /src/erun-skills
COPY Formula /src/Formula
COPY bucket /src/bucket

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/usr/local/share/.cache/yarn \
    cd erun-ui/frontend && \
    yarn typecheck && yarn lint && yarn format:check && yarn test

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-ui && go mod tidy -diff && GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint go tool golangci-lint run ./... && go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path . && \
    cd playwright/fixtures/winstub && GOWORK=off GOOS=windows go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.2.2 run ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-ui && go test -race -count=1 ./...

# Verifies the Windows build links; it is not shipped from here.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-ui && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -tags "desktop,production" -ldflags "-H windowsgui" -o /tmp/erun-app-windows-amd64.exe . && \
    rm /tmp/erun-app-windows-amd64.exe

RUN --mount=type=cache,target=/root/.cache/ms-playwright \
    --mount=type=cache,target=/usr/local/share/.cache/yarn \
    cd erun-ui/playwright && \
    yarn install --frozen-lockfile --prefer-offline --network-timeout 600000 && \
    yarn typecheck && yarn lint && yarn format:check && \
    yarn playwright install chromium && \
    yarn test

# An empty ERUN_VERSION falls back to erun-devops/VERSION.
FROM test AS package

# Installs the foreign target's C toolchain and libraries; a no-op for a native target.
ARG TARGETARCH
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go -C /src/erun-common run ./cmd/debpkg cross-deps --arch "${TARGETARCH}" \
        libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev

COPY LICENSE /src/LICENSE
COPY erun-devops/VERSION /src/erun-devops/VERSION
COPY erun-devops/linux/erun-ui/erun-app.desktop /src/erun-devops/linux/erun-ui/erun-app.desktop
ARG ERUN_VERSION
ARG ERUN_DEB_MAINTAINER="ERun <erun@local>"
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go -C /src/erun-common run ./cmd/debpkg build \
        --name erun-desktop --version "${ERUN_VERSION}" --version-file /src/erun-devops/VERSION \
        --arch "${TARGETARCH}" --maintainer "${ERUN_DEB_MAINTAINER}" \
        --section devel --recommends erun --homepage https://github.com/sophium/erun \
        --description "ERun desktop application" \
        --description-line "The erun-app desktop client for ERun tenants and environments. It runs" \
        --description-line "the erun CLI, which the erun package provides." \
        --cgo --tags desktop,production,webkit2_41 \
        --go-build /src/erun-ui:.:/usr/bin/erun-app:main.buildVersion \
        --shlibdeps /usr/bin/erun-app \
        --file /src/erun-ui/build/appicon.png:/usr/share/pixmaps/erun-app.png \
        --file /src/erun-devops/linux/erun-ui/erun-app.desktop:/usr/share/applications/erun-app.desktop \
        --file /src/LICENSE:/usr/share/doc/erun-desktop/copyright \
        --out /deb

FROM scratch AS dist

COPY --from=package /deb/ /
