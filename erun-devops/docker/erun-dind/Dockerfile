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
