# Blueprint: two-stage image that builds the Docusaurus site, then pushes it
# to Cloudflare Pages via a pinned wrangler. Substitute __MODULE__ with the
# docs module directory name (e.g. acme-docs). Pin node + wrangler so deploys
# stay reproducible.
ARG ERUN_VERSION=1.0.0

# Stage 1: build the Docusaurus static site.
FROM node:20.18.1-alpine AS builder

WORKDIR /src

# Install deps first so source edits don't bust the cache.
COPY __MODULE__/package.json __MODULE__/yarn.lock /src/__MODULE__/

WORKDIR /src/__MODULE__

# YARN_CACHE_FOLDER: this stage runs as root with no HOME override, so yarn
# classic's own default cache dir is $HOME/.cache/yarn (/root/.cache/yarn),
# never the /usr/local/share/.cache/yarn mounted below. Redirecting via env
# var keeps the mount target every other yarn-using Dockerfile in the repo
# already uses.
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

# Copy the rest and build.
COPY __MODULE__ /src/__MODULE__

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn build

# Stage 2: tiny image with pinned wrangler that pushes /site/ to Cloudflare Pages.
FROM node:20.18.1-alpine

ARG WRANGLER_VERSION=3.87.0

RUN apk add --no-cache ca-certificates tini && \
    npm install -g wrangler@${WRANGLER_VERSION} && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=builder --chown=erun:erun /src/__MODULE__/build /site
COPY --chmod=0755 erun-devops/docker/__MODULE__/entrypoint.sh /usr/local/bin/__MODULE__-entrypoint

USER erun
WORKDIR /home/erun

ENV SITE_DIR=/site

ENTRYPOINT ["/sbin/tini", "--", "__MODULE__-entrypoint"]
