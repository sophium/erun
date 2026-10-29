# `dist` builds FROM `test`, so nothing ships unless the tests passed.

# ATLAS_VERSION is shared so the suites run against the atlas the image ships.
# Downloads are pinned by version and by a per-arch SHA-256 their RUN checks before use;
# download_checksum_contract_test.go fails any download that is not.
ARG HELM_VERSION=v4.3.1
ARG HELM_SHA256_AMD64=0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b
ARG HELM_SHA256_ARM64=8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88
ARG TERRAFORM_VERSION=1.17.1
ARG TERRAFORM_SHA256_AMD64=4638628058e71a55212b1c563712eac03b902128f40740f3dc8174df12135061
ARG TERRAFORM_SHA256_ARM64=988e950a1c23058093b0e66ab2d5fb53fc18345ecfbd5871acf51dbac9325edd
ARG ATLAS_VERSION=v1.3.5
ARG ATLAS_SHA256_AMD64=41d2a3c77fab370aead88b9a9faf2bab24d22e3e5728dc2dd7f87404adbce048
ARG ATLAS_SHA256_ARM64=bcdb097755995ef304a08635d38b65e9c2636489f45d0f7c04f2316bbd888ba9
# Keep in sync with @playwright/test in erun-ui/playwright/yarn.lock.
ARG PLAYWRIGHT_VERSION=1.63.0

# Pinned to BUILDPLATFORM so the suites run natively once, not per arch under emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS cli-build

# erun-mcp's attach tests run a real `dtach -A` session.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    apt-get update && apt-get install -y --no-install-recommends dtach

# The integration suite's atlas.sum scenarios run the real `atlas migrate hash`.
ARG BUILDARCH
ARG ATLAS_VERSION
ARG ATLAS_SHA256_AMD64
ARG ATLAS_SHA256_ARM64
RUN case "${BUILDARCH}" in \
        amd64) atlas_sha256="${ATLAS_SHA256_AMD64}" ;; \
        arm64) atlas_sha256="${ATLAS_SHA256_ARM64}" ;; \
        *) echo "unsupported BUILDARCH: ${BUILDARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/atlas "https://release.ariga.io/atlas/atlas-linux-${BUILDARCH}-${ATLAS_VERSION}" && \
    echo "${atlas_sha256}  /tmp/atlas" | sha256sum -c - && \
    install -m 0755 /tmp/atlas /usr/local/bin/atlas && \
    rm /tmp/atlas

WORKDIR /src

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

# What the suites read from the rest of the checkout. Every new
# erun-devops/docker/<image> directory is added here too.
COPY .dockerignore .gitignore /src/
COPY .erun/config.yaml /src/.erun/config.yaml
COPY Formula /src/Formula
COPY bucket /src/bucket
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
COPY erun-devops/e2e/console/Dockerfile /src/erun-devops/e2e/console/Dockerfile
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
    cd erun-common && go mod tidy -diff && go tool golangci-lint run ./... && go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path . && go test -race -count=1 ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-mcp && go mod tidy -diff && go tool golangci-lint run ./... && go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path . && go test -count=1 ./... && \
    CGO_ENABLED=0 GOOS=darwin go build ./... && \
    CGO_ENABLED=0 GOOS=windows go build ./...

# The darwin and windows builds prove the Homebrew and Scoop source builds compile.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-cli && go mod tidy -diff && go tool golangci-lint run ./... && go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path . && go test -count=1 ./... && \
    CGO_ENABLED=0 GOOS=darwin go build ./... && \
    CGO_ENABLED=0 GOOS=windows go build ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    cd erun-integration && go tool golangci-lint run ./... && go run github.com/fe3dback/go-arch-lint@v1.19.0 check --project-path . && go test -count=1 -timeout=45m ./...

FROM cli-test AS test

# ERUN_VERSION falls back to erun-devops/VERSION for a bare docker build.
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
    go build -ldflags "-X github.com/sophium/erun/cmd/shared.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/cmd/shared.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/cmd/shared.buildDate=${ERUN_DATE}" -o /out/erun . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-X github.com/sophium/erun/erun-mcp/tools/shared/buildinfo.buildVersion=${ERUN_VERSION} -X github.com/sophium/erun/erun-mcp/tools/shared/buildinfo.buildCommit=${ERUN_COMMIT} -X github.com/sophium/erun/erun-mcp/tools/shared/buildinfo.buildDate=${ERUN_DATE}" -o /out/emcp ../erun-mcp/cmd/emcp

# Target-arch Go for in-pod builds; not pinned to BUILDPLATFORM since it ships and runs nothing here.
FROM golang:1.26.10 AS goroot

FROM ghcr.io/sophium/erun-ubuntu:noble-20261024.1-1 AS runtime

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.36.6
ARG KUBECTL_SHA256_AMD64=29818b6729dc9262b692307470d0558d027725e738be5dd47c760261868a2634
ARG KUBECTL_SHA256_ARM64=35507ef408c3d38849e5d3203e18f8e861db822aa664101a9ced0869edd06046
ARG HELM_VERSION
ARG HELM_SHA256_AMD64
ARG HELM_SHA256_ARM64
ARG TERRAFORM_VERSION
ARG TERRAFORM_SHA256_AMD64
ARG TERRAFORM_SHA256_ARM64
ARG K9S_VERSION=v0.51.0
ARG K9S_SHA256_AMD64=c3752ad51a5a4015a113819c4eeb6e55a4d0e4b8e652494797532f6fc8161dd7
ARG K9S_SHA256_ARM64=3ee05c82e5f9198928a4e86133608ba6a2c10a2244d6a7789e820f78319d640c
ARG DOCKER_VERSION=29.9.2
ARG DOCKER_SHA256_AMD64=9ac225999fd5e863f7694c728ef23d480616b1e5639328fa309befd4243d69eb
ARG DOCKER_SHA256_ARM64=873b3e0207857c3b2f28392fb922f3ea6efe37620c16d4d4aaf2fef409c90b73
ARG BUILDX_VERSION=v0.38.1
ARG BUILDX_SHA256_AMD64=e25d5cd675092bb5d1d03632fdc46cdf3fede7b514dc5f023a105e00d3ce5ad4
ARG BUILDX_SHA256_ARM64=3b83b3731916ba433feccaaa689fe5bcdc81c641d92c8e6c2ea865dfa141c97a
ARG DIFFT_VERSION=0.70.0
ARG DIFFT_SHA256_AMD64=2997d2bbe620534edbd79b0049f00ce84eef3fedb15c7822456d58e38d8b05c9
ARG DIFFT_SHA256_ARM64=e729684907d67d1a1727a08f443877e19e40eeb2efebcd95c1b8f7fee4284e8e
ARG GH_VERSION=2.102.1
ARG GH_SHA256_AMD64=77b9e297e94c50336823d0f84de7ef1be60cba704601fa0d98f129f47a94987b
ARG GH_SHA256_ARM64=5019b3cc6e8a03bb4d88600f8dbda2add8e6407705c159b0c63151036e77a1af
ARG RIPGREP_VERSION=15.2.0
ARG RIPGREP_SHA256_AMD64=33e15bcf1624b25cdd2a55813a47a2f95dbe126268203e76aa6a585d1e7b149c
ARG RIPGREP_SHA256_ARM64=a740b91c82eaf9914cfedd353572f2791cbe0162c84101ee0951058f4dcbc90d
ARG NODE_VERSION=24.22.1
ARG NODE_SHA256_AMD64=eb6728e77c576a35162c122c45ccadf224d89ff00abc8dae46cc2897c6002d7c
ARG NODE_SHA256_ARM64=33c37be234ba8bee041961047b19160f4d7d0f164e8e246f78bdd10feff148b3
# Replaces Node's bundled npm, whose own dependencies carry advisories.
ARG NPM_VERSION=11.21.1
ARG YARN_VERSION=1.22.22
ARG CODEX_VERSION=0.165.0
ARG CLAUDE_CODE_VERSION=2.1.308
ARG AWS_CLI_VERSION=2.38.5
ARG AWS_CLI_SHA256_AMD64=5def7d1b7e55d892f9a381bb2cb2081a685712464be0d36d5d706af0ac48d0d3
ARG AWS_CLI_SHA256_ARM64=07711a936e9d271045ab937d2c2c5390c25f419e4ba9e25adb9e996031e3f6be
ARG ATLAS_VERSION
ARG ATLAS_SHA256_AMD64
ARG ATLAS_SHA256_ARM64
ARG PLAYWRIGHT_VERSION

USER root

# Wails/webkit CGO build deps come from erun-ubuntu.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists \
    apt-get update && \
    apt-get install -y git bash-completion bubblewrap dpkg-dev iproute2 procps && \
    # ripgrep ships musl only for amd64; the asymmetric triples are deliberate.
    case "${TARGETARCH}" in \
        amd64) arch=amd64; docker_arch=x86_64; node_arch=x64; aws_arch=x86_64; rg_triple=x86_64-unknown-linux-musl; \
            kubectl_sha256="${KUBECTL_SHA256_AMD64}"; atlas_sha256="${ATLAS_SHA256_AMD64}"; helm_sha256="${HELM_SHA256_AMD64}"; \
            terraform_sha256="${TERRAFORM_SHA256_AMD64}"; k9s_sha256="${K9S_SHA256_AMD64}"; difft_sha256="${DIFFT_SHA256_AMD64}"; \
            rg_sha256="${RIPGREP_SHA256_AMD64}"; gh_sha256="${GH_SHA256_AMD64}"; node_sha256="${NODE_SHA256_AMD64}"; aws_sha256="${AWS_CLI_SHA256_AMD64}"; \
            docker_sha256="${DOCKER_SHA256_AMD64}"; buildx_sha256="${BUILDX_SHA256_AMD64}" ;; \
        arm64) arch=arm64; docker_arch=aarch64; node_arch=arm64; aws_arch=aarch64; rg_triple=aarch64-unknown-linux-gnu; \
            kubectl_sha256="${KUBECTL_SHA256_ARM64}"; atlas_sha256="${ATLAS_SHA256_ARM64}"; helm_sha256="${HELM_SHA256_ARM64}"; \
            terraform_sha256="${TERRAFORM_SHA256_ARM64}"; k9s_sha256="${K9S_SHA256_ARM64}"; difft_sha256="${DIFFT_SHA256_ARM64}"; \
            rg_sha256="${RIPGREP_SHA256_ARM64}"; gh_sha256="${GH_SHA256_ARM64}"; node_sha256="${NODE_SHA256_ARM64}"; aws_sha256="${AWS_CLI_SHA256_ARM64}"; \
            docker_sha256="${DOCKER_SHA256_ARM64}"; buildx_sha256="${BUILDX_SHA256_ARM64}" ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" && \
    echo "${kubectl_sha256}  /tmp/kubectl" | sha256sum -c - && \
    install -m 0755 /tmp/kubectl /usr/local/bin/kubectl && \
    curl -fsSL -o /tmp/atlas "https://release.ariga.io/atlas/atlas-linux-${arch}-${ATLAS_VERSION}" && \
    echo "${atlas_sha256}  /tmp/atlas" | sha256sum -c - && \
    install -m 0755 /tmp/atlas /usr/local/bin/atlas && \
    curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${arch}.tar.gz" && \
    echo "${helm_sha256}  /tmp/helm.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    install -m 0755 "/tmp/linux-${arch}/helm" /usr/local/bin/helm && \
    curl -fsSL -o /tmp/terraform.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${arch}.zip" && \
    echo "${terraform_sha256}  /tmp/terraform.zip" | sha256sum -c - && \
    unzip -q /tmp/terraform.zip -d /tmp && \
    install -m 0755 /tmp/terraform /usr/local/bin/terraform && \
    curl -fsSL -o /tmp/k9s.tar.gz "https://github.com/derailed/k9s/releases/download/${K9S_VERSION}/k9s_Linux_${arch}.tar.gz" && \
    echo "${k9s_sha256}  /tmp/k9s.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/k9s.tar.gz -C /tmp && \
    install -m 0755 /tmp/k9s /usr/local/bin/k9s && \
    curl -fsSL -o /tmp/difft.tar.gz "https://github.com/Wilfred/difftastic/releases/download/${DIFFT_VERSION}/difft-${docker_arch}-unknown-linux-gnu.tar.gz" && \
    echo "${difft_sha256}  /tmp/difft.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/difft.tar.gz -C /tmp && \
    install -m 0755 /tmp/difft /usr/local/bin/difft && \
    curl -fsSL -o /tmp/ripgrep.tar.gz "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-${rg_triple}.tar.gz" && \
    echo "${rg_sha256}  /tmp/ripgrep.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/ripgrep.tar.gz -C /tmp && \
    install -m 0755 "/tmp/ripgrep-${RIPGREP_VERSION}-${rg_triple}/rg" /usr/local/bin/rg && \
    curl -fsSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" && \
    echo "${gh_sha256}  /tmp/gh.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/gh.tar.gz -C /tmp && \
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh && \
    curl -fsSL -o /tmp/docker.tgz "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_VERSION}.tgz" && \
    echo "${docker_sha256}  /tmp/docker.tgz" | sha256sum -c - && \
    tar -xzf /tmp/docker.tgz -C /tmp && \
    install -m 0755 /tmp/docker/docker /usr/local/bin/docker && \
    curl -fsSL -o /tmp/docker-buildx "https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}/buildx-${BUILDX_VERSION}.linux-${arch}" && \
    echo "${buildx_sha256}  /tmp/docker-buildx" | sha256sum -c - && \
    install -D -m 0755 /tmp/docker-buildx /usr/local/lib/docker/cli-plugins/docker-buildx && \
    curl -fsSL -o /tmp/node.tar.gz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz" && \
    echo "${node_sha256}  /tmp/node.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/node.tar.gz -C /usr/local --strip-components=1 && \
    curl -fsSL -o /tmp/awscliv2.zip "https://awscli.amazonaws.com/awscli-exe-linux-${aws_arch}-${AWS_CLI_VERSION}.zip" && \
    echo "${aws_sha256}  /tmp/awscliv2.zip" | sha256sum -c - && \
    unzip -q /tmp/awscliv2.zip -d /tmp && \
    /tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli && \
    npm install -g "npm@${NPM_VERSION}" && \
    npm install -g "@openai/codex@${CODEX_VERSION}" "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" "yarn@${YARN_VERSION}" && \
    npm cache clean --force && \
    rm -rf /tmp/kubectl /tmp/atlas /tmp/helm.tar.gz /tmp/terraform.zip /tmp/k9s.tar.gz /tmp/difft.tar.gz /tmp/ripgrep.tar.gz /tmp/gh.tar.gz /tmp/docker.tgz /tmp/docker-buildx /tmp/node.tar.gz /tmp/awscliv2.zip "/tmp/linux-${arch}" "/tmp/gh_${GH_VERSION}_linux_${arch}" "/tmp/ripgrep-${RIPGREP_VERSION}-${rg_triple}" /tmp/terraform /tmp/k9s /tmp/difft /tmp/docker /tmp/aws && \
    groupmod -n erun ubuntu && \
    usermod -l erun -d /home/erun -m -g erun ubuntu && \
    passwd -d erun && \
    mkdir -p /home/erun/git /home/erun/.kube /home/erun/.config/k9s /home/erun/.erun/outputs /etc/erun /etc/bash_completion.d /opt/erun && \
    printf '\n[ -r /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion\n[ -r /etc/bash_completion.d/erun ] && . /etc/bash_completion.d/erun\n[ -r /etc/erun/bash_prompt.sh ] && . /etc/erun/bash_prompt.sh\n' >> /etc/bash.bashrc && \
    chown -R erun:erun /home/erun && \
    chmod 0755 /usr/local/bin/kubectl /usr/local/bin/atlas /usr/local/bin/helm /usr/local/bin/terraform /usr/local/bin/k9s /usr/local/bin/difft /usr/local/bin/rg /usr/local/bin/docker /usr/local/bin/aws

# Chromium system libraries for the erun-ui Playwright suite, via Playwright's own installer
# so Ubuntu library renames are tracked. Separate layer so a version bump rebuilds only this.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists \
    npx --yes "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium && \
    rm -rf /root/.npm

COPY --from=goroot /usr/local/go /usr/local/go
COPY --from=dist --chmod=0755 /out/erun /usr/local/bin/erun
COPY --from=dist --chmod=0755 /out/emcp /usr/local/bin/emcp
COPY --chmod=0644 erun-devops/docker/erun-devops/bash_prompt.sh /etc/erun/bash_prompt.sh
# 0755 because --chmod applies to directories too; the boot installs the files as 0644.
COPY --chmod=0755 erun-skills/skills /etc/erun/skills
COPY --chmod=0755 erun-skills/agents /etc/erun/agents

# A bare `claude` runs erun, which resumes the directory's last conversation.
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

# erun is PID 1: it must reap sshd's orphaned children and forward signals.
ENTRYPOINT ["erun", "runtime-init"]
CMD ["devops"]
