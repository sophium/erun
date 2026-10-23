# The erun-devops runtime image, plus the erun CLI and the emcp server it
# ships:
#
#   cli-build -> cli-test     erun-common, erun-mcp, erun-cli and
#                             erun-integration: native compile, then each
#                             module's own lint and tests, and darwin and
#                             windows builds of erun and emcp
#
# `test` is the stage erun detects, `dist` (FROM test) stamps the version and
# compiles erun and emcp for the target, and `runtime` takes the binaries from
# `dist`. Nothing ships unless the tests passed, and a version bump reruns only
# `dist`. The image's init is erun itself (`erun runtime-init`).

# Pinned tool versions. ATLAS_VERSION is shared by the test stage and the
# runtime image, so the suites run against the atlas the image ships.
ARG HELM_VERSION=v4.2.2
ARG TERRAFORM_VERSION=1.12.2
ARG ATLAS_VERSION=v1.2.0
# Keep in sync with the @playwright/test version in erun-ui/playwright/yarn.lock
# so the installed Chromium system libraries match the browser the suite
# downloads at run time.
ARG PLAYWRIGHT_VERSION=1.63.0

# Pinned to BUILDPLATFORM so the suites run natively once per build instead of
# once per target arch under emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS cli-build

# dtach for erun-mcp's attach tests, which run a real `dtach -A` session.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    apt-get update && apt-get install -y --no-install-recommends dtach

# atlas for the integration suite's gate-merge/merge atlas.sum scenarios, which
# run the real `atlas migrate hash` rather than a stub of its hashing.
ARG BUILDARCH
ARG ATLAS_VERSION
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
    go -C erun-common mod download && \
    go -C erun-mcp mod download && \
    go -C erun-cli mod download && \
    go -C erun-integration mod download

COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp
COPY erun-cli /src/erun-cli
COPY erun-integration /src/erun-integration

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-cli && \
    CGO_ENABLED=0 go build -o /out/erun . && \
    CGO_ENABLED=0 go build -o /out/emcp ../erun-mcp/cmd/emcp

FROM cli-build AS cli-test

# What the suites read from the rest of the checkout: the build-context and
# project config they check, the Dockerfiles and charts erun builds and
# deploys and the packages it releases, the API routes and desktop/console
# sources the surface audits enumerate, and the docs and skill pages the drift
# checks compare against.
# Of this image's own directory only the Dockerfile is copied: it is the one
# file here the suites read. A new erun-devops/docker/<image> directory is added
# here too (TestCLITestStageCopiesEveryDockerDirectory).
COPY .dockerignore .gitignore /src/
COPY .erun/config.yaml /src/.erun/config.yaml
COPY erun-devops/docker/erun-backend-api /src/erun-devops/docker/erun-backend-api
COPY erun-devops/docker/erun-backend-db /src/erun-devops/docker/erun-backend-db
COPY erun-devops/docker/erun-backend-postgres /src/erun-devops/docker/erun-backend-postgres
COPY erun-devops/docker/erun-console /src/erun-devops/docker/erun-console
COPY erun-devops/docker/erun-dind /src/erun-devops/docker/erun-dind
COPY erun-devops/docker/erun-dns01-webhook /src/erun-devops/docker/erun-dns01-webhook
COPY erun-devops/docker/erun-docs /src/erun-devops/docker/erun-docs
COPY erun-devops/docker/erun-oci-registry /src/erun-devops/docker/erun-oci-registry
COPY erun-devops/docker/erun-powerdns /src/erun-devops/docker/erun-powerdns
COPY erun-devops/docker/erun-ubuntu /src/erun-devops/docker/erun-ubuntu
COPY erun-devops/docker/erun-zitadel /src/erun-devops/docker/erun-zitadel
COPY erun-devops/docker/erun-zitadel-login /src/erun-devops/docker/erun-zitadel-login
COPY erun-devops/docker/erun-devops/Dockerfile /src/erun-devops/docker/erun-devops/
COPY erun-devops/linux /src/erun-devops/linux
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
    cd erun-mcp && go tool golangci-lint run ./... && go test -count=1 ./... && \
    CGO_ENABLED=0 GOOS=darwin go build ./... && \
    CGO_ENABLED=0 GOOS=windows go build ./...

# The darwin and windows builds prove the Homebrew and Scoop source builds
# compile; erun-cli and erun-mcp compile erun-common with them.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-cli && go tool golangci-lint run ./... && go test -count=1 ./... && \
    CGO_ENABLED=0 GOOS=darwin go build ./... && \
    CGO_ENABLED=0 GOOS=windows go build ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-integration && go tool golangci-lint run ./... && go test -count=1 -timeout=45m ./...

# The stage erun detects. A further part of this image would join here.
FROM cli-test AS test

# Cross-compiles with CGO disabled, so the compiler runs natively on the build
# host. ERUN_VERSION falls back to erun-devops/VERSION for a bare docker build.
FROM test AS dist

COPY erun-devops/VERSION /src/erun-devops/VERSION
ARG TARGETOS=linux
ARG TARGETARCH
ARG ERUN_COMMIT=""
ARG ERUN_DATE=""
ARG ERUN_VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd erun-cli && \
    ERUN_VERSION="${ERUN_VERSION:-$(tr -d '\n' < /src/erun-devops/VERSION)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/cmd.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${ERUN_DATE}" -o /out/erun . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/erun-mcp.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/erun-mcp.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/erun-mcp.buildDate=${ERUN_DATE}" -o /out/emcp ../erun-mcp/cmd/emcp

# Target-arch Go toolchain for the final image's own `go` binary (needed for
# in-pod contribute-mode builds, e.g. erun-ui). Deliberately not pinned to
# BUILDPLATFORM: this stage ships into the runtime image and runs no RUN
# instructions, so sourcing it costs a per-arch layer pull, not emulation.
FROM golang:1.26.0 AS goroot

FROM ghcr.io/sophium/erun-ubuntu:noble-20260217-1 AS runtime

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.33.1
ARG HELM_VERSION
ARG TERRAFORM_VERSION
ARG K9S_VERSION=v0.50.9
ARG DOCKER_VERSION=28.1.1
ARG BUILDX_VERSION=v0.18.0
ARG DIFFT_VERSION=0.68.0
ARG GH_VERSION=2.74.2
ARG RIPGREP_VERSION=15.1.0
ARG NODE_VERSION=24.14.0
ARG YARN_VERSION=1.22.22
ARG CODEX_VERSION=0.125.0
ARG CLAUDE_CODE_VERSION=2.1.222
ARG AWS_CLI_VERSION=2.32.16
ARG ATLAS_VERSION
ARG PLAYWRIGHT_VERSION

USER root

# Keep external tooling installs pinned and centralized in one layer for traceability.
#
# build-essential + pkg-config + libgtk-3-dev + libwebkit2gtk-4.1-dev +
# libsoup-3.0-dev + dtach are no longer installed here -- they live in
# erun-ubuntu now (see that Dockerfile's own comment), so this stage already
# has them. They exist so the in-pod contribute-mode workflow can rebuild
# erun-ui (the Wails desktop binary) and run it in headless mode against a
# port-forward: Wails' Go bindings link against webkit2gtk on Linux via CGO,
# and without the dev headers `go build -tags desktop,production` fails at
# compile. Cost: roughly +400 MB on the base image. Worth it because the
# contribute-app browser bridge is the only way to test desktop-side
# changes without leaving the env.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists \
    apt-get update && \
    apt-get install -y git bash-completion bubblewrap dpkg-dev iproute2 procps && \
    # ripgrep ships a musl static build for amd64 but only a gnu build for
    # arm64 — the asymmetric triples are deliberate, not a typo.
    case "${TARGETARCH}" in \
        amd64) arch=amd64; docker_arch=x86_64; node_arch=x64; aws_arch=x86_64; rg_triple=x86_64-unknown-linux-musl ;; \
        arm64) arch=arm64; docker_arch=aarch64; node_arch=arm64; aws_arch=aarch64; rg_triple=aarch64-unknown-linux-gnu ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" && \
    curl -fsSL -o /usr/local/bin/atlas "https://release.ariga.io/atlas/atlas-linux-${arch}-${ATLAS_VERSION}" && \
    curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${arch}.tar.gz" && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    install -m 0755 "/tmp/linux-${arch}/helm" /usr/local/bin/helm && \
    curl -fsSL -o /tmp/terraform.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${arch}.zip" && \
    unzip -q /tmp/terraform.zip -d /tmp && \
    install -m 0755 /tmp/terraform /usr/local/bin/terraform && \
    curl -fsSL -o /tmp/k9s.tar.gz "https://github.com/derailed/k9s/releases/download/${K9S_VERSION}/k9s_Linux_${arch}.tar.gz" && \
    tar -xzf /tmp/k9s.tar.gz -C /tmp && \
    install -m 0755 /tmp/k9s /usr/local/bin/k9s && \
    curl -fsSL -o /tmp/difft.tar.gz "https://github.com/Wilfred/difftastic/releases/download/${DIFFT_VERSION}/difft-${docker_arch}-unknown-linux-gnu.tar.gz" && \
    tar -xzf /tmp/difft.tar.gz -C /tmp && \
    install -m 0755 /tmp/difft /usr/local/bin/difft && \
    curl -fsSL -o /tmp/ripgrep.tar.gz "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-${rg_triple}.tar.gz" && \
    tar -xzf /tmp/ripgrep.tar.gz -C /tmp && \
    install -m 0755 "/tmp/ripgrep-${RIPGREP_VERSION}-${rg_triple}/rg" /usr/local/bin/rg && \
    curl -fsSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" && \
    tar -xzf /tmp/gh.tar.gz -C /tmp && \
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh && \
    curl -fsSL -o /tmp/docker.tgz "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_VERSION}.tgz" && \
    tar -xzf /tmp/docker.tgz -C /tmp && \
    install -m 0755 /tmp/docker/docker /usr/local/bin/docker && \
    curl -fsSL -o /tmp/docker-buildx "https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}/buildx-${BUILDX_VERSION}.linux-${arch}" && \
    install -D -m 0755 /tmp/docker-buildx /usr/local/lib/docker/cli-plugins/docker-buildx && \
    curl -fsSL -o /tmp/node.tar.gz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz" && \
    tar -xzf /tmp/node.tar.gz -C /usr/local --strip-components=1 && \
    curl -fsSL -o /tmp/awscliv2.zip "https://awscli.amazonaws.com/awscli-exe-linux-${aws_arch}-${AWS_CLI_VERSION}.zip" && \
    unzip -q /tmp/awscliv2.zip -d /tmp && \
    /tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli && \
    npm install -g "@openai/codex@${CODEX_VERSION}" "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" "yarn@${YARN_VERSION}" && \
    npm cache clean --force && \
    rm -rf /tmp/helm.tar.gz /tmp/terraform.zip /tmp/k9s.tar.gz /tmp/difft.tar.gz /tmp/ripgrep.tar.gz /tmp/gh.tar.gz /tmp/docker.tgz /tmp/docker-buildx /tmp/node.tar.gz /tmp/awscliv2.zip "/tmp/linux-${arch}" "/tmp/gh_${GH_VERSION}_linux_${arch}" "/tmp/ripgrep-${RIPGREP_VERSION}-${rg_triple}" /tmp/terraform /tmp/k9s /tmp/difft /tmp/docker /tmp/aws && \
    groupmod -n erun ubuntu && \
    usermod -l erun -d /home/erun -m -g erun ubuntu && \
    passwd -d erun && \
    mkdir -p /home/erun/git /home/erun/.kube /home/erun/.config/k9s /home/erun/.erun/outputs /etc/erun /etc/bash_completion.d /opt/erun && \
    printf '\n[ -r /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion\n[ -r /etc/bash_completion.d/erun ] && . /etc/bash_completion.d/erun\n[ -r /etc/erun/bash_prompt.sh ] && . /etc/erun/bash_prompt.sh\n' >> /etc/bash.bashrc && \
    chown -R erun:erun /home/erun && \
    chmod 0755 /usr/local/bin/kubectl /usr/local/bin/atlas /usr/local/bin/helm /usr/local/bin/terraform /usr/local/bin/k9s /usr/local/bin/difft /usr/local/bin/rg /usr/local/bin/docker /usr/local/bin/aws

# Chromium runtime libraries for the erun-ui Playwright suite. The build deps
# in the layer above only cover compiling erun-app (Wails links webkit2gtk via
# CGO); the headless browser the Playwright suite drives needs its
# own shared libraries (libnss3, libnspr4, and the GTK/X/ALSA runtime set).
# Without them, in-pod contribute-mode can build erun-ui but cannot run its
# mandatory UI tests. Use Playwright's own dependency installer, pinned to the
# suite's version, so the package set stays correct across Ubuntu library
# renames (the 24.04 `t64` transition) instead of hand-maintaining a fragile
# list. The browser binary itself is installed without root by
# `playwright install chromium`; only these system libs need root, so they
# belong in the image. Kept as a separate layer (not folded into the tooling
# layer above) so a Playwright version bump invalidates only this layer, not
# the much larger tooling install above it.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists \
    npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium && \
    rm -rf /root/.npm

COPY --from=goroot /usr/local/go /usr/local/go
COPY --from=dist --chmod=0755 /out/erun /usr/local/bin/erun
COPY --from=dist --chmod=0755 /out/emcp /usr/local/bin/emcp
COPY --chmod=0644 erun-devops/docker/erun-devops/bash_prompt.sh /etc/erun/bash_prompt.sh
# Directories need the execute/search bit so the unprivileged erun user can
# enter them; the boot (`erun runtime-init`) copies each skill into
# ~/.claude/skills and ~/.codex/skills as 0644 files. 0644 here would leave the
# dirs un-searchable and the install would silently copy nothing.
COPY --chmod=0755 erun-skills/skills /etc/erun/skills
# --chmod applies to the directory too, and an agents directory without the
# execute bit cannot be entered: every agent file in it then fails to open and
# none is installed. The boot installs the files themselves as 0644.
COPY --chmod=0755 erun-skills/agents /etc/erun/agents

# A bare `claude` runs erun, which resumes the directory's last conversation
# and execs the real Claude Code binary the npm install left in node_modules.
RUN /usr/local/bin/erun completion bash > /etc/bash_completion.d/erun && \
    chmod 0644 /etc/bash_completion.d/erun && \
    ln -sf /usr/local/bin/erun /usr/local/bin/claude

ENV HOME=/home/erun
ENV PATH=/usr/local/go/bin:${PATH}
ENV GOPATH=/home/erun/go
ENV KUBECONFIG=/home/erun/.kube/config
ENV DOCKER_HOST=unix:///var/run/docker.sock
ENV DOCKER_BUILDKIT=1
WORKDIR /home/erun

USER erun

EXPOSE 17000 17033 17022 9100

# erun is PID 1: `erun runtime-init` boots the environment, reaps orphaned
# children (the daemonised sshd's per-connection children reparent to PID 1;
# with workspace-sync opening an SSH connection every couple of seconds, an
# unreaped PID 1 accumulates zombies until no container on the node can fork),
# forwards termination signals and exits with the code of what it ran.
ENTRYPOINT ["erun", "runtime-init"]
CMD ["devops"]
