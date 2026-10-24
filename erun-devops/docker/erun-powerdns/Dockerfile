# Keep in sync with the sibling VERSION file and the chart's default tag.
ARG POWERDNS_VERSION=4.9.3
FROM powerdns/pdns-auth-49:${POWERDNS_VERSION}

# Override the upstream's inherited source label so the package links to erun.
LABEL org.opencontainers.image.source=https://github.com/sophium/erun
