# The deployed console's e2e suite, run by `erun e2e` from the repository
# root as the build context, after a deploy and never as part of a build:
#
#   docker build --target e2e --no-cache-filter e2e --output type=cacheonly \
#     --build-arg ERUN_E2E_BASE_URL=https://<console host> \
#     --build-arg ERUN_E2E_VERSION=<deployed version> \
#     -f erun-devops/e2e/console/Dockerfile .
#
# The image tag matches the @playwright/test version yarn.lock resolves, so the
# browsers baked into it are the revision the test runner expects.
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

# The suite's own checks, then the suite. The e2e operator's account and the
# environment the attach test drives come from `.erun/config.yaml` e2e.secrets;
# a test whose secret is not supplied skips and names it, and the
# unauthenticated checks always run.
RUN --mount=type=secret,id=e2e_login_name,env=E2E_LOGIN_NAME \
    --mount=type=secret,id=e2e_login_password,env=E2E_LOGIN_PASSWORD \
    --mount=type=secret,id=e2e_attach_environment,env=E2E_ATTACH_ENVIRONMENT \
    yarn typecheck && yarn lint && yarn format:check && yarn test
