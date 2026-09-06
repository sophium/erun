ARG DOCKER_VERSION=28.1.1
FROM docker:${DOCKER_VERSION}-dind

ENV DOCKER_TLS_CERTDIR=

# docker:dind defaults to iptables-nft, which fails on cluster kernels whose
# nf_tables protocol version does not match Alpine's userspace
# ("iptables: Failed to initialize nft: Protocol not supported"). Switch the
# symlinks to the legacy xtables backend so dockerd can program rules.
RUN apk add --no-cache iptables-legacy \
    && ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables \
    && ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables \
    && ln -sf /usr/sbin/iptables-legacy-save /usr/sbin/iptables-save \
    && ln -sf /usr/sbin/iptables-legacy-restore /usr/sbin/iptables-restore \
    && ln -sf /usr/sbin/ip6tables-legacy-save /usr/sbin/ip6tables-save \
    && ln -sf /usr/sbin/ip6tables-legacy-restore /usr/sbin/ip6tables-restore

# Without this, BuildKit's embedded worker runs its stock GC policy and
# evicts cache mounts (go-build, yarn, golangci-lint, playwright, ...) by LRU
# purely on its own schedule, with no regard for how much disk is actually
# free — the same "evict while there's room to spare" failure this image's
# release-time headroom check (erun-common's ensureReleaseDiskHeadroom) exists
# to avoid on the release side. 32GB is comfortably above that check's own
# 20GiB floor, so BuildKit's own GC never undercuts it, while staying modest
# against the node disk this pod's docker-state PVC shares with the other
# environments scheduled on it.
COPY erun-devops/docker/erun-dind/daemon.json /etc/docker/daemon.json
