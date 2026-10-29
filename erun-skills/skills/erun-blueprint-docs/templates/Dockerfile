# Blueprint: substitute __MODULE__ with the docs module directory name.
ARG ERUN_VERSION=1.0.0

FROM node:24.22.1-alpine AS build

WORKDIR /src

COPY __MODULE__/package.json __MODULE__/yarn.lock /src/__MODULE__/

WORKDIR /src/__MODULE__

# Root's yarn cache defaults to $HOME/.cache/yarn, not the cache mount below.
ENV YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

# `onBrokenLinks: throw` makes a broken link fail the build.
COPY __MODULE__ /src/__MODULE__

RUN yarn build

# The runtime stage copies from `test`, so no image exists unless checks pass.
FROM build AS test

RUN yarn typecheck

FROM node:24.22.1-alpine AS runtime

ARG WRANGLER_VERSION=4.154.0

# Only this install runs npm, and the npm the base image bundles carries advisories
# of its own, so neither it nor its cache ships.
RUN --mount=type=cache,target=/root/.npm \
    apk add --no-cache ca-certificates tini && \
    npm install -g wrangler@${WRANGLER_VERSION} && \
    rm -rf /usr/local/lib/node_modules/npm /usr/local/bin/npm /usr/local/bin/npx && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=test --chown=erun:erun /src/__MODULE__/build /site
COPY --chmod=0755 erun-devops/docker/__MODULE__/entrypoint.sh /usr/local/bin/__MODULE__-entrypoint

USER erun
WORKDIR /home/erun

ENV SITE_DIR=/site

ENTRYPOINT ["/sbin/tini", "--", "__MODULE__-entrypoint"]
