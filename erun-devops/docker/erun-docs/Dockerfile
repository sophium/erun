ARG ERUN_VERSION=1.0.0

# Pinned to the build host's platform: the site it emits is static assets with
# no architecture, so it is built (and checked) once natively.
FROM --platform=$BUILDPLATFORM node:20.18.1-alpine AS deps

WORKDIR /src/erun-docs

# Install deps first so source edits don't bust the cache.
COPY erun-docs/package.json erun-docs/yarn.lock /src/erun-docs/

# YARN_CACHE_FOLDER: yarn classic otherwise caches under $HOME/.cache/yarn,
# not the mounted /usr/local/share/.cache/yarn.
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

COPY erun-docs /src/erun-docs

# The site's own check is this image's gate: the builder depends on this stage
# through `COPY --from=test`, so no image is produced when it fails.
FROM deps AS test

RUN yarn typecheck && touch /test-ok

FROM deps AS builder

COPY --from=test /test-ok /tmp/erun-test-ok

RUN yarn build

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
