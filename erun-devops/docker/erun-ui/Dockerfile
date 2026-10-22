# erun-ui builds and tests itself here: the `test` stage only calls the
# module's own commands (build.sh, go test, the Playwright suite's run.sh),
# and the final stage copies from it, so no image exists unless they passed.
#
# Pinned to BUILDPLATFORM so the suite runs natively once per build. erun-app
# links webkit through CGO and cannot be cross-compiled for another Linux
# architecture, so the Linux binary is named for the architecture it was built
# on rather than assumed to match the image's.

# Same version erun-ui/playwright pins, so install-deps installs the Chromium
# runtime libraries that browser revision needs.
ARG PLAYWRIGHT_VERSION=1.63.0

# Pinned node/yarn toolchain copied into the test stage; no RUN of its own.
FROM --platform=$BUILDPLATFORM node:22.14-bookworm AS node

FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

# Wails' Linux backend (gtk3, webkit2gtk-4.1, libsoup-3.0) for the CGO build.
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

ARG PLAYWRIGHT_VERSION
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium

WORKDIR /src

# The wails CLI generate-wailsjs.sh runs, at the version erun-ui's go.mod pins.
COPY erun-ui/go.mod erun-ui/go.sum erun-ui/go.work erun-ui/go.work.sum /src/erun-ui/
COPY erun-common/go.mod erun-common/go.sum /src/erun-common/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-ui && \
    go install "github.com/wailsapp/wails/v2/cmd/wails@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"

COPY package.json yarn.lock /src/
# The root Yarn workspace lists erun-console; its manifest keeps the frozen
# lockfile consistent without bringing its source in.
COPY erun-console/package.json /src/erun-console/package.json
COPY erun-kit /src/erun-kit
COPY erun-common /src/erun-common
COPY erun-skills /src/erun-skills
# build.sh stamps this version; buildstamp_test.go also reads the
# package-manager formulae.
COPY erun-devops/VERSION /src/erun-devops/VERSION
COPY Formula /src/Formula
COPY bucket /src/bucket
COPY erun-ui /src/erun-ui

ENV YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn

# Frontend typecheck, lint, format:check and unit tests, the Go lint, then the
# bundle and the Linux binary at bin/erun-app.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    --mount=type=cache,target=/usr/local/share/.cache/yarn \
    GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint ./erun-ui/build.sh

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-ui && go test -race -count=1 ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-ui && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -tags "desktop,production" -ldflags "-H windowsgui" -o /out/erun-app-windows-amd64.exe .

# The Playwright suite against the binary build.sh produced above.
RUN --mount=type=cache,target=/root/.cache/ms-playwright \
    --mount=type=cache,target=/usr/local/share/.cache/yarn \
    ./erun-ui/playwright/run.sh

ARG BUILDARCH
RUN cp erun-ui/bin/erun-app "/out/erun-app-linux-${BUILDARCH}"

FROM scratch

COPY --from=test /out/ /
