# Passthrough wrapper over the upstream Zitadel Login V2 image, following the
# wrap-and-pin pattern (erun-devops/AGENTS.md). Zitadel v4 core serves no
# interactive login, so this is not an optional add-on: without it the OIDC
# authorize endpoint has no page to render. The pinned version must match
# erun-zitadel's — one Zitadel line, two images.
ARG ZITADEL_VERSION=v4.15.3
FROM ghcr.io/zitadel/zitadel-login:${ZITADEL_VERSION}
