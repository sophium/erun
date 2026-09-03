#!/bin/sh
set -eu

cd /opt/erun-backend-db

if [ -z "${ERUN_DATABASE_URL:-}" ]; then
    echo "ERUN_DATABASE_URL is required" >&2
    exit 1
fi

dry_run=false
if [ "${ERUN_RETENTION_DRY_RUN:-false}" = "true" ]; then
    dry_run=true
fi

for policy in retention/*.sql; do
    echo "running retention sweep ${policy} (dry_run=${dry_run})"
    psql -v ON_ERROR_STOP=1 -v "dry_run=${dry_run}" -f "${policy}" "${ERUN_DATABASE_URL}"
done
