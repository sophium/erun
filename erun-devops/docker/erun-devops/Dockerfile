FROM golang:1.25.5 AS builder

WORKDIR /src

# Copy module manifests first so dependency downloads stay cached when only source changes.
COPY erun-cli/go.mod erun-cli/go.sum erun-cli/go.work erun-cli/go.work.sum /src/erun-cli/
COPY erun-common/go.mod /src/erun-common/go.mod
COPY erun-mcp/go.mod erun-mcp/go.sum /src/erun-mcp/
COPY erun-devops/VERSION /src/erun-devops/VERSION

WORKDIR /src/erun-cli

ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY erun-cli /src/erun-cli
COPY erun-common /src/erun-common
COPY erun-mcp /src/erun-mcp

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ERUN_VERSION="$(tr -d '\n' < /src/erun-devops/VERSION)" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/cmd.buildVersion=${ERUN_VERSION}" -o /out/erun . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/erun-mcp.buildVersion=${ERUN_VERSION}" -o /out/emcp ../erun-mcp/cmd/emcp

FROM erunpaas/erun-ubuntu:noble-20260217

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.33.1
ARG HELM_VERSION=v3.18.2
ARG TERRAFORM_VERSION=1.12.2
ARG K9S_VERSION=v0.50.9
ARG DOCKER_VERSION=28.1.1
ARG BUILDX_VERSION=v0.24.0
ARG DIFFT_VERSION=0.68.0

USER root

# Keep external tooling installs pinned and centralized in one layer for traceability.
RUN apt-get update && \
    apt-get install -y git bash-completion && \
    rm -rf /var/lib/apt/lists/* && \
    case "${TARGETARCH}" in \
        amd64) arch=amd64; docker_arch=x86_64 ;; \
        arm64) arch=arm64; docker_arch=aarch64 ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" && \
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
    curl -fsSL -o /tmp/docker.tgz "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_VERSION}.tgz" && \
    tar -xzf /tmp/docker.tgz -C /tmp && \
    install -m 0755 /tmp/docker/docker /usr/local/bin/docker && \
    mkdir -p /usr/local/lib/docker/cli-plugins /usr/local/libexec/docker/cli-plugins && \
    curl -fsSL -o /usr/local/lib/docker/cli-plugins/docker-buildx "https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}/buildx-${BUILDX_VERSION}.linux-${arch}" && \
    ln -sf /usr/local/lib/docker/cli-plugins/docker-buildx /usr/local/libexec/docker/cli-plugins/docker-buildx && \
    rm -rf /tmp/helm.tar.gz /tmp/terraform.zip /tmp/k9s.tar.gz /tmp/difft.tar.gz /tmp/docker.tgz "/tmp/linux-${arch}" /tmp/terraform /tmp/k9s /tmp/difft /tmp/docker && \
    groupmod -n erun ubuntu && \
    usermod -l erun -d /home/erun -m -g erun ubuntu && \
    mkdir -p /home/erun/git /home/erun/.kube /home/erun/.config/k9s /etc/erun /etc/bash_completion.d && \
    printf '\n[ -r /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion\n[ -r /etc/bash_completion.d/erun ] && . /etc/bash_completion.d/erun\n[ -r /etc/erun/bash_prompt.sh ] && . /etc/erun/bash_prompt.sh\n' >> /etc/bash.bashrc && \
    chown -R erun:erun /home/erun && \
    chmod 0755 /usr/local/bin/kubectl /usr/local/bin/helm /usr/local/bin/terraform /usr/local/bin/k9s /usr/local/bin/difft /usr/local/bin/docker /usr/local/lib/docker/cli-plugins/docker-buildx /usr/local/libexec/docker/cli-plugins/docker-buildx

COPY --from=builder --chmod=0755 /out/erun /usr/local/bin/erun
COPY --from=builder --chmod=0755 /out/emcp /usr/local/bin/emcp
COPY --chmod=0755 erun-devops/docker/erun-devops/entrypoint.sh /usr/local/bin/erun-devops-entrypoint
COPY --chmod=0644 erun-devops/docker/erun-devops/bash_prompt.sh /etc/erun/bash_prompt.sh

RUN /usr/local/bin/erun completion bash > /etc/bash_completion.d/erun && \
    chmod 0644 /etc/bash_completion.d/erun

ENV HOME=/home/erun
ENV KUBECONFIG=/home/erun/.kube/config
ENV DOCKER_HOST=unix:///var/run/docker.sock
ENV DOCKER_BUILDKIT=1
WORKDIR /home/erun

USER erun

EXPOSE 17000

ENTRYPOINT ["erun-devops-entrypoint"]
CMD ["--host", "0.0.0.0"]
