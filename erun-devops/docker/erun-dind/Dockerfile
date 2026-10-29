ARG DOCKER_VERSION=29.9.2
FROM docker:${DOCKER_VERSION}-dind

# Override the upstream's inherited source label so the package links to erun.
LABEL org.opencontainers.image.source=https://github.com/sophium/erun

ENV DOCKER_TLS_CERTDIR=

# iptables-nft fails on kernels whose nf_tables version mismatches Alpine's userspace.
# The brotli-libs and libcurl floors carry fixes the upstream image predates; apk fails without them.
RUN apk add --no-cache iptables-legacy 'brotli-libs>=1.2.1-r0' 'libcurl>=8.23.0-r0' \
    && ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables \
    && ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables \
    && ln -sf /usr/sbin/iptables-legacy-save /usr/sbin/iptables-save \
    && ln -sf /usr/sbin/iptables-legacy-restore /usr/sbin/iptables-restore \
    && ln -sf /usr/sbin/ip6tables-legacy-save /usr/sbin/ip6tables-save \
    && ln -sf /usr/sbin/ip6tables-legacy-restore /usr/sbin/ip6tables-restore

# GC limits live inside gc.policy: docker 28 ignored the deprecated top-level defaultKeepStorage,
# and `dockerd --validate` accepts unknown gc keys, so only a running daemon proves them.
# minFreeSpace stays above kubelet's ~23GB eviction threshold so GC reclaims first.
# containerd-snapshotter is off because docker 29 moves only a fresh data root onto the
# containerd image store, which keeps each layer both compressed and unpacked. Off, every
# docker-state volume, new or carried over, stays on the overlay2 store erun's disk bounds
# and per-arch push were built against.
COPY erun-devops/docker/erun-dind/daemon.json /etc/docker/daemon.json
