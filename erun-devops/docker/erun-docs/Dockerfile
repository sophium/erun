ARG ERUN_VERSION=1.0.0

# Stages: `build` installs the site's dependencies and runs `yarn build`,
# `test` runs the site's own check on top of that, and `runtime` publishes the
# built site, taken from `test`, with wrangler. Because the site comes through
# `test`, no image exists unless the check passed.
#
# Pinned to the build host's platform: the site is static assets with no
# architecture, so it is built (and checked) once natively.
FROM --platform=$BUILDPLATFORM node:20.18.1-alpine AS build

WORKDIR /src/erun-docs

# Install deps first so source edits don't bust the cache.
COPY erun-docs/package.json erun-docs/yarn.lock /src/erun-docs/

# YARN_CACHE_FOLDER: yarn classic otherwise caches under $HOME/.cache/yarn,
# not the mounted /usr/local/share/.cache/yarn.
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

COPY erun-docs /src/erun-docs

RUN yarn build

FROM build AS test

RUN yarn typecheck

# A tiny image with pinned wrangler that pushes /site/ to Cloudflare Pages.
FROM node:20.18.1-alpine AS runtime

ARG WRANGLER_VERSION=3.87.0

RUN apk add --no-cache ca-certificates tini && \
    npm install -g wrangler@${WRANGLER_VERSION} && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=test --chown=erun:erun /src/erun-docs/build /site
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
