# Passthrough wrapper over the upstream Zitadel Login V2 image, following the
# wrap-and-pin pattern (erun-devops/AGENTS.md). Zitadel v4 core serves no
# interactive login, so this is not an optional add-on: without it the OIDC
# authorize endpoint has no page to render. The pinned version must match
# erun-zitadel's — one Zitadel line, two images.
ARG ZITADEL_VERSION=v4.15.3
FROM ghcr.io/zitadel/zitadel-login:${ZITADEL_VERSION}

# Reclaim provenance from the upstream image. A passthrough FROM inherits the
# upstream's org.opencontainers.image.source, which points at the vendor's
# repository, so without this override the published package attributes itself
# to them and links there instead of to erun. Registry *visibility* is a
# separate axis and is not label-driven (see erun-devops/AGENTS.md).
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
