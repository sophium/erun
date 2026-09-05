# Passthrough wrapper over the upstream Zitadel core image, following the
# wrap-and-pin pattern (erun-devops/AGENTS.md). The pinned version lives in the
# sibling VERSION file and in the chart's default tag; they must move together,
# and together with erun-zitadel-login, which has to run the same Zitadel line.
ARG ZITADEL_VERSION=v4.15.3
FROM ghcr.io/zitadel/zitadel:${ZITADEL_VERSION}

# Reclaim provenance from the upstream image. A passthrough FROM inherits the
# upstream's org.opencontainers.image.source, which points at the vendor's
# repository, so without this override the published package attributes itself
# to them and links there instead of to erun. Registry *visibility* is a
# separate axis and is not label-driven (see erun-devops/AGENTS.md).
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
