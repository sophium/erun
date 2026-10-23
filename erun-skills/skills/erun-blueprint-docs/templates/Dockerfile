# Blueprint: builds the Docusaurus site, checks it, then ships it in a tiny
# image with a pinned wrangler that pushes it to Cloudflare Pages. Substitute
# __MODULE__ with the docs module directory name (e.g. acme-docs). Pin node +
# wrangler so deploys stay reproducible.
#
# Stages: `build` builds the site, `test` runs the site's own checks on top of
# it, and `runtime` copies the built site from `test`. Because the shipped site
# comes through `test`, BuildKit cannot skip the checks: no image exists
# unless they passed.
ARG ERUN_VERSION=1.0.0

FROM node:20.18.1-alpine AS build

WORKDIR /src

# Install deps first so source edits don't bust the cache.
COPY __MODULE__/package.json __MODULE__/yarn.lock /src/__MODULE__/

WORKDIR /src/__MODULE__

# YARN_CACHE_FOLDER: this stage runs as root with no HOME override, so yarn
# classic's own default cache dir is $HOME/.cache/yarn (/root/.cache/yarn),
# never the /usr/local/share/.cache/yarn mounted below. Redirecting via env
# var keeps the mount target every other yarn-using Dockerfile in the repo
# already uses.
ENV YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

# Copy the rest and build. `onBrokenLinks: throw` makes a broken link fail
# here.
COPY __MODULE__ /src/__MODULE__

RUN yarn build

# The site's own checks, run against the built site above.
FROM build AS test

RUN yarn typecheck

# Tiny image with pinned wrangler that pushes /site/ to Cloudflare Pages.
FROM node:20.18.1-alpine AS runtime

ARG WRANGLER_VERSION=3.87.0

RUN apk add --no-cache ca-certificates tini && \
    npm install -g wrangler@${WRANGLER_VERSION} && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun

COPY --from=test --chown=erun:erun /src/__MODULE__/build /site
COPY --chmod=0755 erun-devops/docker/__MODULE__/entrypoint.sh /usr/local/bin/__MODULE__-entrypoint

USER erun
WORKDIR /home/erun

ENV SITE_DIR=/site

ENTRYPOINT ["/sbin/tini", "--", "__MODULE__-entrypoint"]
