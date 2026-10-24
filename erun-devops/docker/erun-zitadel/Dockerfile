# Version must match the sibling VERSION file, the chart's default tag, and erun-zitadel-login.
ARG ZITADEL_VERSION=v4.15.3
FROM ghcr.io/zitadel/zitadel:${ZITADEL_VERSION}

# Overrides the source label inherited from the upstream image so the package links to erun.
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
