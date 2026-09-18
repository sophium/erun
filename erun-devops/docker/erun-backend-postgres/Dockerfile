ARG POSTGRES_VERSION=18.3
FROM postgres:${POSTGRES_VERSION}

# Reclaim provenance from the upstream image. A passthrough FROM inherits the
# upstream's org.opencontainers.image.source, which points at the vendor's
# repository, so without this override the published package attributes itself
# to them and links there instead of to erun. Registry *visibility* is a
# separate axis and is not label-driven (see erun-devops/AGENTS.md).
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
