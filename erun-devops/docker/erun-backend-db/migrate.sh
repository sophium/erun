#!/bin/sh
set -eu

cd /opt/erun-backend-db

if [ -z "${ERUN_DATABASE_URL:-}" ]; then
    echo "ERUN_DATABASE_URL is required" >&2
    exit 1
fi

echo "applying PostgreSQL migrations with Atlas"
apply_log="$(mktemp)"
if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>"$apply_log"; then
    cat "$apply_log" >&2
    if grep -q "connected database is not clean" "$apply_log"; then
        baseline_version="${ERUN_DATABASE_BASELINE_VERSION:-20260503143000}"
        echo "baselining existing PostgreSQL schema at Atlas migration ${baseline_version}"
        atlas migrate set "$baseline_version" --env default --url "${ERUN_DATABASE_URL}"
        atlas migrate apply --env default --url "${ERUN_DATABASE_URL}"
    else
        exit 1
    fi
fi
rm -f "$apply_log"
