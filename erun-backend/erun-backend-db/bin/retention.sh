#!/bin/sh
set -eu

cd /opt/erun-backend-db

if [ -z "${ERUN_DATABASE_URL:-}" ]; then
    echo "ERUN_DATABASE_URL is required" >&2
    exit 1
fi

# Default to report-only: an unconfigured run (ERUN_RETENTION_DRY_RUN unset,
# whether because the chart's own retention.dryRun default was bypassed or
# because this entrypoint was invoked directly) must never delete.
dry_run=true
if [ "${ERUN_RETENTION_DRY_RUN:-true}" = "false" ]; then
    dry_run=false
fi

echo "running retention sweep (dry_run=${dry_run})"

# psql runs every -c/-f given to it, in order, on one connection -- so all
# policy files execute inside a single session, and a session-scoped
# advisory lock taken before the first one and released after the last
# covers the whole sweep. This guards against a second run (a manual
# invocation, or any out-of-band trigger) racing this one's deletes;
# concurrencyPolicy: Forbid on the CronJob already stops the scheduler
# itself from overlapping its own runs, but the lock is what makes that true
# regardless of how a run was started. A session-scoped lock also releases
# automatically if the connection drops, so a crashed run never leaves the
# lock held for the next attempt.
lock_sql="DO \$lock\$ BEGIN IF NOT pg_try_advisory_lock(hashtext('erun_retention')) THEN RAISE EXCEPTION 'retention sweep already running (advisory lock held)'; END IF; END \$lock\$;"
unlock_sql="SELECT pg_advisory_unlock(hashtext('erun_retention'));"

set -- -v ON_ERROR_STOP=1 -v "dry_run=${dry_run}" -c "${lock_sql}"
for policy in retention/*.sql; do
    set -- "$@" -f "${policy}"
done
set -- "$@" -c "${unlock_sql}"

psql "$@" "${ERUN_DATABASE_URL}"
