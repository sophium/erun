#!/bin/sh
set -eu

cd /opt/erun-backend-db

if [ -z "${ERUN_DATABASE_URL:-}" ]; then
    echo "ERUN_DATABASE_URL is required" >&2
    exit 1
fi

# Report-only unless explicitly disabled: an unconfigured run must never delete.
dry_run=true
if [ "${ERUN_RETENTION_DRY_RUN:-true}" = "false" ]; then
    dry_run=false
fi

echo "running retention sweep (dry_run=${dry_run})"

# psql runs every -c/-f on one connection, so a session advisory lock spans the whole sweep,
# excludes any concurrent run, and releases itself if the connection drops.
lock_sql="DO \$lock\$ BEGIN IF NOT pg_try_advisory_lock(hashtext('erun_retention')) THEN RAISE EXCEPTION 'retention sweep already running (advisory lock held)'; END IF; END \$lock\$;"
unlock_sql="SELECT pg_advisory_unlock(hashtext('erun_retention'));"

set -- -v ON_ERROR_STOP=1 -v "dry_run=${dry_run}" -c "${lock_sql}"
for policy in retention/*.sql; do
    set -- "$@" -f "${policy}"
done
set -- "$@" -c "${unlock_sql}"

psql "$@" "${ERUN_DATABASE_URL}"
