ARG ERUN_VERSION=1.0.0

# `runtime` takes the site from `test`, so no image exists unless the check passed.
FROM --platform=$BUILDPLATFORM node:24.22.1-alpine AS build

WORKDIR /src/erun-docs

COPY erun-docs/package.json erun-docs/yarn.lock /src/erun-docs/

# yarn classic otherwise caches under $HOME, outside the cache mount.
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

COPY erun-docs /src/erun-docs

RUN yarn build

FROM build AS test

RUN yarn typecheck

FROM node:24.22.1-alpine AS runtime

ARG WRANGLER_VERSION=3.87.0

RUN apk add --no-cache ca-certificates tini && \
    npm install -g wrangler@${WRANGLER_VERSION} && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=test --chown=erun:erun /src/erun-docs/build /site
COPY --chmod=0755 erun-devops/docker/erun-docs/entrypoint.sh /usr/local/bin/erun-docs-entrypoint

# Served at GET /version.json: a static site has no process to report its version.
ARG ERUN_VERSION
RUN printf '{"version":"%s"}\n' "${ERUN_VERSION}" > /site/version.json && \
    chown erun:erun /site/version.json

USER erun
WORKDIR /home/erun

ENV SITE_DIR=/site

ENTRYPOINT ["/sbin/tini", "--", "erun-docs-entrypoint"]
