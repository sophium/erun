#!/bin/sh
set -eu

# Translate optional env-driven config into eapi flags. Mirrors the
# dispatch the runtime erun-devops-entrypoint used to perform when
# erun-backend-api shared the runtime image.
if [ -n "${ERUN_AWS_IDENTITY_STORE_REGION:-}" ]; then
    set -- --aws-identity-store-region "${ERUN_AWS_IDENTITY_STORE_REGION}" "$@"
fi
if [ -n "${ERUN_AWS_IDENTITY_STORE_ID:-}" ]; then
    set -- --aws-identity-store-id "${ERUN_AWS_IDENTITY_STORE_ID}" "$@"
fi
if [ -n "${ERUN_OIDC_ALLOWED_ISSUERS:-}" ]; then
    set -- --oidc-allowed-issuers "${ERUN_OIDC_ALLOWED_ISSUERS}" "$@"
fi
if [ -n "${ERUN_OIDC_ALLOWED_AUDIENCES:-}" ]; then
    set -- --oidc-allowed-audiences "${ERUN_OIDC_ALLOWED_AUDIENCES}" "$@"
fi

echo "starting erun API on ${ERUN_API_HOST:-0.0.0.0}:${ERUN_API_PORT:-17033}"

exec eapi \
    --host "${ERUN_API_HOST:-0.0.0.0}" \
    --port "${ERUN_API_PORT:-17033}" \
    "$@"
