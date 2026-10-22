# Shared by the test stage's tools and the final image's, so the charts and
# modules are tested with the same helm/terraform the runtime image ships.
ARG HELM_VERSION=v4.2.2
ARG TERRAFORM_VERSION=1.12.2
ARG ATLAS_VERSION=v1.2.0
# Keep in sync with the @playwright/test version in erun-ui/playwright/yarn.lock
# so the installed Chromium system libraries match the browser the suite
# downloads at run time.
ARG PLAYWRIGHT_VERSION=1.63.0

# erun and emcp come from the erun-cli image built in the same run; this FROM
# is the only use of ERUN_VERSION here, so `erun build` orders erun-cli first.
ARG ERUN_VERSION=1.0.0
FROM ghcr.io/sophium/erun-cli:${ERUN_VERSION} AS erun-cli

# This image's own tests: the runtime scripts it bakes, the charts it deploys
# and the Terraform modules tenants apply. The final stage depends on the
# marker, so no image is produced when they fail.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test

ARG BUILDARCH
ARG HELM_VERSION
ARG TERRAFORM_VERSION
RUN apt-get update && apt-get install -y --no-install-recommends dtach jq nodejs unzip && \
    curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${BUILDARCH}.tar.gz" && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    install -m 0755 "/tmp/linux-${BUILDARCH}/helm" /usr/local/bin/helm && \
    curl -fsSL -o /tmp/terraform.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${BUILDARCH}.zip" && \
    unzip -q /tmp/terraform.zip -d /tmp && \
    install -m 0755 /tmp/terraform /usr/local/bin/terraform && \
    rm -rf /tmp/helm.tar.gz "/tmp/linux-${BUILDARCH}" /tmp/terraform.zip /tmp/terraform

WORKDIR /src

COPY erun-devops/docker /src/erun-devops/docker
COPY erun-devops/k8s /src/erun-devops/k8s
COPY erun-devops/terraform-erun/modules /src/erun-devops/terraform-erun/modules
# erun-backend-db-chart_test.sh checks the chart against this page's claims.
COPY erun-docs/docs/deployment/data-retention.md /src/erun-docs/docs/deployment/data-retention.md

RUN for t in erun-devops/docker/erun-devops/*_test.sh erun-devops/k8s/*_test.sh; do \
        echo ">> ${t}" && sh "${t}" || exit 1; \
    done && \
    for m in erun-devops/terraform-erun/modules/*/; do \
        [ -d "${m}tests" ] || continue; \
        echo ">> ${m}" && (cd "${m}" && terraform init -backend=false -input=false && terraform test) || exit 1; \
    done && \
    touch /test-ok

# Target-arch Go toolchain for the final image's own `go` binary (needed for
# in-pod contribute-mode builds, e.g. erun-ui). Deliberately not pinned to
# BUILDPLATFORM: this stage ships into the runtime image and runs no RUN
# instructions, so sourcing it costs a per-arch layer pull, not emulation.
FROM golang:1.26.0 AS goroot

FROM ghcr.io/sophium/erun-ubuntu:noble-20260217-1

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
    apt-get install -y git bash-completion bubblewrap dpkg-dev iproute2 procps tini && \
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
    mv /usr/local/bin/codex /usr/local/bin/codex-real && \
    mv /usr/local/bin/claude /usr/local/bin/claude-real && \
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
# CGO); the headless browser that `erun-ui/playwright/run.sh` drives needs its
# own shared libraries (libnss3, libnspr4, and the GTK/X/ALSA runtime set).
# Without them, in-pod contribute-mode can build erun-ui but cannot run its
# mandatory UI tests. Use Playwright's own dependency installer, pinned to the
# suite's version, so the package set stays correct across Ubuntu library
# renames (the 24.04 `t64` transition) instead of hand-maintaining a fragile
# list. The browser binary itself is installed without root by run.sh's
# `playwright install chromium`; only these system libs need root, so they
# belong in the image. Kept as a separate layer (not folded into the tooling
# layer above) so a Playwright version bump invalidates only this layer, not
# the much larger tooling install above it.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists \
    npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium && \
    rm -rf /root/.npm

COPY --from=goroot /usr/local/go /usr/local/go
COPY --from=test /test-ok /tmp/erun-test-ok
COPY --from=erun-cli --chmod=0755 /erun /usr/local/bin/erun
COPY --from=erun-cli --chmod=0755 /emcp /usr/local/bin/emcp
COPY --chmod=0755 erun-devops/docker/erun-devops/entrypoint.sh /usr/local/bin/erun-devops-entrypoint
COPY --chmod=0755 erun-devops/docker/erun-devops/skills-install.sh /usr/local/bin/erun-install-skills
COPY --chmod=0755 erun-devops/docker/erun-devops/agents-install.sh /usr/local/bin/erun-install-agents
# erun-link-release symlinks a sourceless runtime env's git folder at
# /opt/erun/release so a tenant devops image can surface baked artifacts there
# (outside the /home/erun PVC, which would otherwise shadow them).
COPY --chmod=0755 erun-devops/docker/erun-devops/release-link.sh /usr/local/bin/erun-link-release
# erun-prune-sessions clears persistent desktop session sockets left with no
# dtach server behind them, so a replaced pod never presents a leftover socket
# as a running session.
COPY --chmod=0755 erun-devops/docker/erun-devops/session-prune.sh /usr/local/bin/erun-prune-sessions
# erun-trim-cache bounds the go build cache under ~/.cache by size, evicting its
# least recently used entries. The entrypoint runs it in the background; the
# chart carries the cap, because the pod cannot read a node-local volume's size
# from the mounted filesystem.
COPY --chmod=0755 erun-devops/docker/erun-devops/cache-trim.sh /usr/local/bin/erun-trim-cache
# erun-adopt-worktree runs in the chart's init container, where the home volume
# and the worktree claim are staged side by side, so an environment whose
# checkout predates the claim is moved onto it instead of being masked by it.
COPY --chmod=0755 erun-devops/docker/erun-devops/worktree-adopt.sh /usr/local/bin/erun-adopt-worktree
# erun-oidc-bootstrap runs as the erun-zitadel chart's oidc-bootstrap sidecar,
# reconciling the platform's OIDC applications against the Management API.
COPY --chmod=0755 erun-devops/docker/erun-devops/oidc-bootstrap.sh /usr/local/bin/erun-oidc-bootstrap
COPY --chmod=0755 erun-devops/docker/erun-devops/codex-wrapper.sh /usr/local/bin/codex
COPY --chmod=0755 erun-devops/docker/erun-devops/claude-wrapper.sh /usr/local/bin/claude
COPY --chmod=0644 erun-devops/docker/erun-devops/bash_prompt.sh /etc/erun/bash_prompt.sh
# Directories need the execute/search bit so the unprivileged erun user can
# enter them; the entrypoint copies each skill into ~/.claude/skills and
# ~/.codex/skills (re-chmodding the destination files to 0644). 0644 here would
# leave the dirs un-searchable and the install would silently copy nothing.
COPY --chmod=0755 erun-skills/skills /etc/erun/skills
# Reusable agents are flat <name>.md files (no subdirectory to need the
# execute bit for), so 0644 is enough here.
COPY --chmod=0644 erun-skills/agents /etc/erun/agents

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

EXPOSE 17000 17033 17022 9100

# Run PID 1 under tini so orphaned children are reaped. The entrypoint daemonises
# sshd, whose per-connection session children reparent to PID 1 when they finish;
# with workspace-sync opening an SSH connection every couple of seconds, an
# unreaped PID 1 accumulates thousands of <defunct> sshd zombies until the node's
# process table is exhausted and no container can fork (cluster-wide crashloop).
ENTRYPOINT ["/usr/bin/tini", "--", "erun-devops-entrypoint"]
CMD ["devops"]
