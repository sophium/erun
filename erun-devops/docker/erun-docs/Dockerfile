ARG ERUN_VERSION=1.0.0

# Stage 1: build the Docusaurus static site.
# Pinned to the build host's platform: the site it emits is static assets with
# no architecture, so building it once natively beats building it again under
# emulation for the foreign arch every multi-arch build.
FROM --platform=$BUILDPLATFORM node:20.18.1-alpine AS builder

WORKDIR /src

# Install deps first so source edits don't bust the cache.
COPY erun-docs/package.json erun-docs/yarn.lock /src/erun-docs/

WORKDIR /src/erun-docs

# YARN_CACHE_FOLDER: this stage runs as root with no HOME override, so yarn
# classic's own default cache dir is $HOME/.cache/yarn (/root/.cache/yarn),
# never the /usr/local/share/.cache/yarn mounted below -- verified against
# the repo's own yarn (getCacheDir() falls back to $HOME/.cache/yarn) and
# reproduced live (a real yarn install with the mount target unset resolved
# to /root/.cache/yarn). Redirecting via env var keeps the mount target that
# every other yarn-using Dockerfile in the repo already uses.
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

# Copy the rest and build.
COPY erun-docs /src/erun-docs

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn build

# Stage 2: tiny image with pinned wrangler that pushes /site/ to Cloudflare Pages.
FROM node:20.18.1-alpine

ARG WRANGLER_VERSION=3.87.0

RUN apk add --no-cache ca-certificates tini && \
    npm install -g wrangler@${WRANGLER_VERSION} && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=builder --chown=erun:erun /src/erun-docs/build /site
COPY --chmod=0755 erun-devops/docker/erun-docs/entrypoint.sh /usr/local/bin/erun-docs-entrypoint

# Stamps the build actually being published, read back at GET /version.json once
# Cloudflare Pages serves it — the docs site's counterpart to erun-console's
# GET /version.json and erun-backend-api's GET /v1/platform "version" field.
# Written into /site itself so `wrangler pages deploy` uploads it as a plain
# static file alongside the Docusaurus output; a static site has no server
# process to answer a version query any other way.
ARG ERUN_VERSION
RUN printf '{"version":"%s"}\n' "${ERUN_VERSION}" > /site/version.json && \
    chown erun:erun /site/version.json

USER erun
WORKDIR /home/erun

ENV SITE_DIR=/site

ENTRYPOINT ["/sbin/tini", "--", "erun-docs-entrypoint"]
