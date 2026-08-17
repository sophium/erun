# Passthrough wrapper over the upstream Zitadel core image, following the
# wrap-and-pin pattern (erun-devops/AGENTS.md). The pinned version lives in the
# sibling VERSION file and in the chart's default tag; they must move together,
# and together with erun-zitadel-login, which has to run the same Zitadel line.
ARG ZITADEL_VERSION=v4.15.3
FROM ghcr.io/zitadel/zitadel:${ZITADEL_VERSION}
