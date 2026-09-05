ARG ERUN_VERSION=1.0.0

# Stage 1: build the Vite/React SPA. Node 22 rather than 20: installing from the
# workspace root (see below) resolves the whole workspace's devDependencies, and
# @testing-library/jest-dom@6 declares engines node >=22. Pinned to the same
# 22.14 line the erun-devops test stage already uses, so the two agree. Bumping
# the image is the honest fix here — --ignore-engines would silently install a
# module against an engine it rejects.
# Pinned to the build host's platform: the bundle it emits is static assets with
# no architecture, so building it once natively beats building it again under
# emulation for the foreign arch every multi-arch build.
FROM --platform=$BUILDPLATFORM node:22.14-alpine AS builder

WORKDIR /src

# Install from the WORKSPACE ROOT, not from erun-console alone. erun-console
# depends on erun-kit ("erun-kit": "*"), which is a yarn workspace sibling
# resolved through the root node_modules symlink rather than published to a
# registry. Installing erun-console on its own makes yarn try to fetch it and
# fail with `https://registry.yarnpkg.com/erun-kit: Not found` — which is
# exactly how this image broke when the kit was extracted, and why no gate
# caught it: every other check runs at the root, where the symlink exists, and
# this image build is the only place the console is installed in isolation.
#
# All four workspace manifests are copied because yarn 1 resolves the whole
# workspace set from the root lockfile; omitting one makes --frozen-lockfile
# unsatisfiable. Manifests first, sources after, so a source edit does not bust
# the dependency layer.
COPY package.json yarn.lock /src/
COPY erun-kit/package.json /src/erun-kit/
COPY erun-console/package.json /src/erun-console/
COPY erun-ui/frontend/package.json /src/erun-ui/frontend/

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

# erun-kit's package.json main points at ./src/index.ts, so the console's vite
# build compiles the kit from source and the kit needs no build step of its own.
COPY erun-kit /src/erun-kit
COPY erun-console /src/erun-console

WORKDIR /src/erun-console

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn build

# Stage 2: a small static server that also proxies /v1 to the API, so the
# console is served same-origin with erun-backend-api (its production design —
# see erun-console/vite.config.ts) and no browser CORS preflight is ever
# needed (the API sets no CORS headers by design).
FROM nginx:1.27.4-alpine3.21

ARG ERUN_VERSION

COPY --from=builder /src/erun-console/dist /usr/share/nginx/html
COPY erun-devops/docker/erun-console/default.conf.template /etc/nginx/templates/default.conf.template

# Stamps the build actually serving this container, read back at
# GET /version.json (see default.conf.template) — the console's counterpart to
# erun-backend-api's GET /v1/platform "version" field, since a static SPA has
# no server process to bake a -ldflags version into.
RUN printf '{"version":"%s"}\n' "${ERUN_VERSION}" > /usr/share/nginx/html/version.json

# Rendered from the template at container start by the base image's own
# entrypoint (docker-entrypoint.d/20-envsubst-on-templates.sh substitutes every
# ${VAR} that is actually set in the container's environment — nginx's own
# runtime variables like $host/$uri are untouched since they are never OS env
# vars). The chart sets these from the API Service's name/port; the defaults
# here just make a bare `docker run` serve something sane locally.
ENV API_PROXY_HOST=127.0.0.1 \
    API_PROXY_PORT=17033 \
    NGINX_PORT=8080

EXPOSE 8080
