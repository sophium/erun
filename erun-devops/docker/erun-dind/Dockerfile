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
# to avoid on the release side.
#
# reservedSpace/maxUsedSpace/minFreeSpace inside gc.policy, not the
# deprecated top-level defaultKeepStorage: the latter is ignored by the docker
# 28 this image pins, and the ceiling read as configured while enforcing
# nothing — measured at 76-90GB of build cache per environment against a
# nominal 32GB, on a node whose four environments then exhausted a 435GB disk,
# tripped kubelet's ephemeral-storage eviction and took every pod on it down
# at once.
#
# `dockerd --validate` is not evidence that any of these keys is honoured: it
# reports "configuration OK" for a config whose gc block contains a key that
# does not exist at all (verified against this image). That is exactly why the
# deprecated setting survived unnoticed, and why a first attempt at this fix
# that put the new keys at the gc top level rather than inside a policy entry
# also read as configured while the cache stayed near 80GB. The only
# meaningful check is watching the cache converge on a running daemon.
#
# The figures are sized against that node rather than picked round: four
# environments at maxUsedSpace 40GB is 160GB of cache, which leaves room for
# images and worktrees on 435GB. minFreeSpace 30GB keeps GC firing above
# kubelet's own ~23GB eviction threshold, so the daemon reclaims before the
# kubelet evicts. reservedSpace 16GB is the floor GC will not cut below,
# comfortably above ensureReleaseDiskHeadroom's own 20GiB free-disk floor
# being a separate concern.
COPY erun-devops/docker/erun-dind/daemon.json /etc/docker/daemon.json
