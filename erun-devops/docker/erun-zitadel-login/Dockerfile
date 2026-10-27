# Required: Zitadel v4 core serves no interactive login page. Keep the version in
# lockstep with erun-zitadel.
ARG ZITADEL_VERSION=v4.20.0
FROM ghcr.io/zitadel/zitadel-login:${ZITADEL_VERSION}

# Overrides the source label inherited from the upstream image so the package links to erun.
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
