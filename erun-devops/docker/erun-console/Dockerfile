ARG ERUN_VERSION=1.0.0

# Install from the WORKSPACE ROOT, not from erun-console alone. erun-console
# depends on erun-kit ("erun-kit": "*"), a yarn workspace sibling resolved
# through the root node_modules symlink rather than published to a registry;
# installing the console on its own makes yarn try to fetch it and fail with
# `https://registry.yarnpkg.com/erun-kit: Not found`. All workspace manifests
# are copied because yarn 1 resolves the whole set from the root lockfile;
# omitting one makes --frozen-lockfile unsatisfiable.
#
# Node 22 because @testing-library/jest-dom@6 declares engines node >=22.
# Pinned to the build host's platform: the bundle is static assets with no
# architecture, so it is built (and tested) once natively.
#
# YARN_CACHE_FOLDER: yarn classic otherwise caches under $HOME/.cache/yarn,
# not the mounted /usr/local/share/.cache/yarn.
FROM --platform=$BUILDPLATFORM node:22.14-alpine AS deps

WORKDIR /src

COPY package.json yarn.lock /src/
COPY erun-kit/package.json /src/erun-kit/
COPY erun-console/package.json /src/erun-console/
COPY erun-ui/frontend/package.json /src/erun-ui/frontend/

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

# erun-kit's package.json main points at ./src/index.ts, so the console compiles
# the kit from source and the kit needs no build step of its own.
COPY erun-kit /src/erun-kit
COPY erun-console /src/erun-console

# The console's and the kit's own checks are this image's gate: the builder
# depends on this stage through `COPY --from=test`, so no image is produced
# when they fail.
FROM deps AS test

RUN cd /src/erun-kit && \
    yarn typecheck && yarn lint && yarn format:check && yarn test && \
    cd /src/erun-console && \
    yarn typecheck && yarn lint && yarn format:check && yarn test && \
    touch /test-ok

FROM deps AS builder

COPY --from=test /test-ok /tmp/erun-test-ok

WORKDIR /src/erun-console

RUN yarn build

# Stage 2: a small static server that also proxies /v1 to the API, so the
# console is served same-origin with erun-backend-api (its production design —
# see erun-console/vite.config.ts) and no browser CORS preflight is ever
# needed (the API sets no CORS headers by design).
FROM nginx:1.27.4-alpine3.21

COPY --from=builder /src/erun-console/dist /usr/share/nginx/html
COPY erun-devops/docker/erun-console/default.conf.template /etc/nginx/templates/default.conf.template

# Stamps this instance's brand into the served index.html before nginx starts,
# from the same platform.brand erun-backend-api serves as GET /v1/platform's
# "brand" (the chart sets both from that one value, see
# erun-devops/k8s/erun-console/templates/console.yaml). Without it the page
# would paint the bundled product default and then repaint with the instance's
# own name as discovery resolved — an identity change on every load. Runs
# before the base image's own 20-envsubst-on-templates.sh by name order.
COPY erun-devops/docker/erun-console/docker-entrypoint.d/05-platform-brand.sh /docker-entrypoint.d/05-platform-brand.sh
RUN chmod +x /docker-entrypoint.d/05-platform-brand.sh

# Stamps the build actually serving this container, read back at
# GET /version.json (see default.conf.template) — the console's counterpart to
# erun-backend-api's GET /v1/platform "version" field, since a static SPA has
# no server process to bake a -ldflags version into.
ARG ERUN_VERSION
RUN printf '{"version":"%s"}\n' "${ERUN_VERSION}" > /usr/share/nginx/html/version.json

# Rendered from the template at container start by the base image's own
# entrypoint (docker-entrypoint.d/20-envsubst-on-templates.sh substitutes every
# ${VAR} that is actually set in the container's environment — nginx's own
# runtime variables like $host/$uri are untouched since they are never OS env
# vars). The chart sets these from the API Service's name/port; the defaults
# here just make a bare `docker run` serve something sane locally.
ENV API_PROXY_HOST=127.0.0.1 \
    API_PROXY_PORT=17033 \
    NGINX_PORT=8080 \
    ERUN_PLATFORM_BRAND=

EXPOSE 8080
