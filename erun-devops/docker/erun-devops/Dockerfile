FROM golang:1.26.0 AS builder

WORKDIR /src

# Copy module manifests first so dependency downloads stay cached when only source changes.
COPY erun-cli/go.mod erun-cli/go.sum erun-cli/go.work erun-cli/go.work.sum /src/erun-cli/
COPY erun-common/go.mod /src/erun-common/go.mod
COPY erun-mcp/go.mod erun-mcp/go.sum /src/erun-mcp/
COPY erun-devops/VERSION /src/erun-devops/VERSION

WORKDIR /src/erun-cli

ARG TARGETOS=linux
ARG TARGETARCH
ARG ERUN_VERSION
ARG ERUN_COMMIT=""
ARG ERUN_DATE=""
ARG GOLANGCI_LINT_VERSION=v2.2.2

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-cli /src/erun-cli
COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ERUN_VERSION="${ERUN_VERSION:-$(tr -d '\n' < /src/erun-devops/VERSION)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/cmd.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${ERUN_DATE}" -o /out/erun . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/erun-mcp.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/erun-mcp.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/erun-mcp.buildDate=${ERUN_DATE}" -o /out/emcp ../erun-mcp/cmd/emcp

# Build golangci-lint from source against this stage's Go toolchain so
# the lint binary's embedded analyzer libraries (go/ast, go/types,
# go/parser) always match the Go version targeted by go.mod. The
# upstream prebuilt tarball is compiled with whatever Go the
# golangci-lint maintainers used at release time, which drifts behind
# erun's go.mod bumps and aborts pre-commit with "go language version
# used to build golangci-lint is lower than the targeted Go version".
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    GOBIN=/out go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"

FROM ghcr.io/sophium/erun-ubuntu:noble-20260217

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.33.1
ARG HELM_VERSION=v3.18.2
ARG TERRAFORM_VERSION=1.12.2
ARG K9S_VERSION=v0.50.9
ARG DOCKER_VERSION=28.1.1
ARG BUILDX_VERSION=v0.18.0
ARG DIFFT_VERSION=0.68.0
ARG GH_VERSION=2.74.2
ARG NODE_VERSION=24.14.0
ARG YARN_VERSION=1.22.22
ARG CODEX_VERSION=0.125.0
ARG CLAUDE_CODE_VERSION=2.1.156
ARG AWS_CLI_VERSION=2.32.16
ARG ATLAS_VERSION=v1.2.0
# Keep in sync with the @playwright/test version resolved in
# erun-ui/playwright/yarn.lock so the installed Chromium system libraries match
# the browser the suite downloads at run time.
ARG PLAYWRIGHT_VERSION=1.60.0

USER root

# Keep external tooling installs pinned and centralized in one layer for traceability.
#
# build-essential + pkg-config + libgtk-3-dev + libwebkit2gtk-4.1-dev exist
# so the in-pod contribute-mode workflow can rebuild erun-ui (the Wails
# desktop binary) and run it in headless mode against a port-forward.
# Wails' Go bindings link against webkit2gtk on Linux via CGO; without
# the dev headers `go build -tags desktop,production` fails at compile.
# Cost: roughly +400 MB on the final image. Worth it because the
# contribute-app browser bridge is the only way to test desktop-side
# changes without leaving the env.
RUN apt-get update && \
    apt-get install -y git bash-completion bubblewrap dpkg-dev procps \
        build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev && \
    rm -rf /var/lib/apt/lists/* && \
    case "${TARGETARCH}" in \
        amd64) arch=amd64; docker_arch=x86_64; node_arch=x64; aws_arch=x86_64 ;; \
        arm64) arch=arm64; docker_arch=aarch64; node_arch=arm64; aws_arch=aarch64 ;; \
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
    mv /usr/local/bin/codex /usr/local/bin/codex-real && \
    mv /usr/local/bin/claude /usr/local/bin/claude-real && \
    npm cache clean --force && \
    rm -rf /tmp/helm.tar.gz /tmp/terraform.zip /tmp/k9s.tar.gz /tmp/difft.tar.gz /tmp/gh.tar.gz /tmp/docker.tgz /tmp/docker-buildx /tmp/node.tar.gz /tmp/awscliv2.zip "/tmp/linux-${arch}" "/tmp/gh_${GH_VERSION}_linux_${arch}" /tmp/terraform /tmp/k9s /tmp/difft /tmp/docker /tmp/aws && \
    groupmod -n erun ubuntu && \
    usermod -l erun -d /home/erun -m -g erun ubuntu && \
    passwd -d erun && \
    mkdir -p /home/erun/git /home/erun/.kube /home/erun/.config/k9s /etc/erun /etc/bash_completion.d && \
    printf '\n[ -r /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion\n[ -r /etc/bash_completion.d/erun ] && . /etc/bash_completion.d/erun\n[ -r /etc/erun/bash_prompt.sh ] && . /etc/erun/bash_prompt.sh\n' >> /etc/bash.bashrc && \
    chown -R erun:erun /home/erun && \
    chmod 0755 /usr/local/bin/kubectl /usr/local/bin/atlas /usr/local/bin/helm /usr/local/bin/terraform /usr/local/bin/k9s /usr/local/bin/difft /usr/local/bin/docker /usr/local/bin/aws

# Chromium runtime libraries for the erun-ui Playwright suite. The build deps
# in the layer above only cover compiling erun-app (Wails links webkit2gtk via
# CGO); the headless browser that `erun-ui/playwright/run.sh` drives needs its
# own shared libraries (libnss3, libnspr4, and the GTK/X/ALSA runtime set).
# Without them, in-pod contribute-mode can build erun-ui but cannot run its
# mandatory UI tests. Use Playwright's own dependency installer, pinned to the
# suite's version, so the package set stays correct across Ubuntu library
# renames (the 24.04 `t64` transition) instead of hand-maintaining a fragile
# list. The browser binary itself is installed without root by run.sh's
# `playwright install chromium`; only these system libs need root, so they
# belong in the image. Kept as a separate layer (not folded into the tooling
# layer above) because install-deps runs its own apt-get update after that
# layer has already cleared the apt lists.
RUN npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium && \
    rm -rf /var/lib/apt/lists/* /root/.npm

COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder --chmod=0755 /out/erun /usr/local/bin/erun
COPY --from=builder --chmod=0755 /out/emcp /usr/local/bin/emcp
COPY --from=builder --chmod=0755 /out/golangci-lint /usr/local/bin/golangci-lint
COPY --chmod=0755 erun-devops/docker/erun-devops/entrypoint.sh /usr/local/bin/erun-devops-entrypoint
COPY --chmod=0755 erun-devops/docker/erun-devops/codex-wrapper.sh /usr/local/bin/codex
COPY --chmod=0755 erun-devops/docker/erun-devops/claude-wrapper.sh /usr/local/bin/claude
COPY --chmod=0644 erun-devops/docker/erun-devops/bash_prompt.sh /etc/erun/bash_prompt.sh
COPY --chmod=0644 erun-skills/skills /etc/erun/skills

RUN /usr/local/bin/erun completion bash > /etc/bash_completion.d/erun && \
    chmod 0644 /etc/bash_completion.d/erun

ENV HOME=/home/erun
ENV PATH=/usr/local/go/bin:${PATH}
ENV GOPATH=/home/erun/go
ENV KUBECONFIG=/home/erun/.kube/config
ENV DOCKER_HOST=unix:///var/run/docker.sock
ENV DOCKER_BUILDKIT=1
WORKDIR /home/erun

USER erun

EXPOSE 17000 17033 17022

ENTRYPOINT ["erun-devops-entrypoint"]
CMD ["devops"]
