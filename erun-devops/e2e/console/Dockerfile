# Run by `erun e2e` against a deployed console, never during a build.
# The image tag must match the @playwright/test version in yarn.lock.
FROM mcr.microsoft.com/playwright:v1.62.1-noble AS deps

ENV YARN_CACHE_FOLDER=/usr/local/share/.cache/yarn

WORKDIR /e2e

COPY erun-devops/e2e/console/package.json erun-devops/e2e/console/yarn.lock /e2e/
RUN --mount=type=cache,target=/usr/local/share/.cache/yarn \
    yarn install --frozen-lockfile --non-interactive

FROM deps AS e2e

COPY erun-devops/e2e/console/playwright.config.ts erun-devops/e2e/console/tsconfig.json \
     erun-devops/e2e/console/eslint.config.mjs erun-devops/e2e/console/.prettierrc.json /e2e/
COPY erun-devops/e2e/console/tests /e2e/tests

ARG ERUN_E2E_BASE_URL
ARG ERUN_E2E_VERSION

# Secrets come from `.erun/config.yaml` e2e.secrets; a test whose secret is missing skips.
RUN --mount=type=secret,id=e2e_login_name,env=E2E_LOGIN_NAME \
    --mount=type=secret,id=e2e_login_password,env=E2E_LOGIN_PASSWORD \
    --mount=type=secret,id=e2e_attach_environment,env=E2E_ATTACH_ENVIRONMENT \
    yarn typecheck && yarn lint && yarn format:check && yarn test
