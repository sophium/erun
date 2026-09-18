# Passthrough wrapper over the upstream PowerDNS Authoritative Server image,
# following the wrap-and-pin pattern (erun-devops/AGENTS.md). The pinned
# version lives in the sibling VERSION file and in the chart's default tag;
# they must move together. The 4.9 line ships as repo `pdns-auth-49`.
ARG POWERDNS_VERSION=4.9.3
FROM powerdns/pdns-auth-49:${POWERDNS_VERSION}

# Reclaim provenance from the upstream image. A passthrough FROM inherits the
# upstream's org.opencontainers.image.source, which points at the vendor's
# repository, so without this override the published package attributes itself
# to them and links there instead of to erun. Registry *visibility* is a
# separate axis and is not label-driven (see erun-devops/AGENTS.md).
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
