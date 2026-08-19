ARG ERUN_VERSION=1.0.0

# Stage 1: build the Vite/React SPA. Needs a newer Node 20 than erun-docs pins
# (20.18.1) — @vitejs/plugin-react@6 requires >=20.19.0.
FROM node:20.19.0-alpine AS builder

WORKDIR /src

# Install deps first so source edits don't bust the cache.
COPY erun-console/package.json erun-console/yarn.lock /src/erun-console/

WORKDIR /src/erun-console

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

COPY erun-console /src/erun-console

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn build

# Stage 2: a small static server that also proxies /v1 to the API, so the
# console is served same-origin with erun-backend-api (its production design —
# see erun-console/vite.config.ts) and no browser CORS preflight is ever
# needed (the API sets no CORS headers by design).
FROM nginx:1.27.4-alpine3.21

COPY --from=builder /src/erun-console/dist /usr/share/nginx/html
COPY erun-devops/docker/erun-console/default.conf.template /etc/nginx/templates/default.conf.template

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
