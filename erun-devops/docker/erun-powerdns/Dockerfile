# Keep in sync with the sibling VERSION file and the chart's default tag.
ARG POWERDNS_VERSION=4.9.18
FROM powerdns/pdns-auth-49:${POWERDNS_VERSION}

# Override the upstream's inherited source label so the package links to erun.
LABEL org.opencontainers.image.source=https://github.com/sophium/erun

# The upstream image predates Debian's libbrotli1 decoder fix; fail the build if apt cannot supply it.
USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends --only-upgrade libbrotli1 && \
    dpkg --compare-versions "$(dpkg-query -W -f='${Version}' libbrotli1)" ge 1.1.0-2+deb13u1 && \
    rm -rf /var/lib/apt/lists/*
USER pdns
