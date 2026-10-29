ARG ERUN_VERSION=1.0.0

# Install from the workspace root: erun-kit is an unpublished workspace sibling,
# and --frozen-lockfile needs every workspace manifest.
# Node 22: @testing-library/jest-dom@6 requires it. Debian, not alpine: Playwright
# ships no Chromium for musl. Static assets, so built once on the build platform.
FROM --platform=$BUILDPLATFORM node:22.23.4-bookworm AS build

# yarn classic otherwise caches under $HOME, outside the cache mount.
ENV YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright

WORKDIR /src

# Ahead of the workspace install so it does not invalidate the browser download.
COPY erun-console/playwright/package.json erun-console/playwright/yarn.lock /src/erun-console/playwright/
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    cd /src/erun-console/playwright && \
    yarn install --frozen-lockfile --non-interactive && \
    yarn playwright install --with-deps chromium

COPY package.json yarn.lock /src/
COPY erun-kit/package.json /src/erun-kit/
COPY erun-console/package.json /src/erun-console/
COPY erun-ui/frontend/package.json /src/erun-ui/frontend/

RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

COPY erun-kit /src/erun-kit
COPY erun-console /src/erun-console

WORKDIR /src/erun-console

RUN yarn build

# The runtime stage copies the bundle from here, so no image exists unless these pass.
FROM build AS test

RUN cd /src/erun-kit && \
    yarn typecheck && yarn lint && yarn format:check && yarn test && \
    cd /src/erun-console && \
    yarn typecheck && yarn lint && yarn format:check && yarn test && \
    cd /src/erun-console/playwright && \
    yarn typecheck && yarn lint && yarn format:check && \
    yarn playwright test tests/landing-no-horizontal-scroll.spec.ts

# Proxies /v1 so the console is same-origin with the API, which sets no CORS headers.
FROM nginx:1.30.6-alpine3.24-slim AS runtime

# -slim ships no brotli-libs, so no Brotli decoder; fail if a base bump brings it back.
# The musl probe fails first if apk itself is gone, which would otherwise read as absent.
RUN apk info -e musl >/dev/null && ! apk info -e brotli-libs >/dev/null

COPY --from=test /src/erun-console/dist /usr/share/nginx/html
COPY erun-devops/docker/erun-console/default.conf.template /etc/nginx/templates/default.conf.template

# Stamps the instance brand into index.html so the first paint matches GET /v1/platform.
COPY erun-devops/docker/erun-console/docker-entrypoint.d/05-platform-brand.sh /docker-entrypoint.d/05-platform-brand.sh
RUN chmod +x /docker-entrypoint.d/05-platform-brand.sh

# Served at GET /version.json: a static SPA has no process to bake a version into.
ARG ERUN_VERSION
RUN printf '{"version":"%s"}\n' "${ERUN_VERSION}" > /usr/share/nginx/html/version.json

# The base image's envsubst renders the template from these; the chart overrides them.
ENV API_PROXY_HOST=127.0.0.1 \
    API_PROXY_PORT=17033 \
    NGINX_PORT=8080 \
    ERUN_PLATFORM_BRAND=

EXPOSE 8080
