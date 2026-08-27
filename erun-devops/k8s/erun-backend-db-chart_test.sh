#!/bin/sh

# Locks the erun-backend-db chart's two migration mechanisms: the existing
# post-install,post-upgrade hook Job (migrate-job.yaml, runs once per Helm
# operation) and the repair CronJob (migrate-repair-cronjob.yaml) added so a
# database that loses its schema by any means outside a Helm operation is
# re-migrated on its own, instead of staying schema-less until someone
# happens to deploy.
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh:
# helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-backend-db"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-backend-db-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

render() {
    out="${work_root}/$1.yaml"
    shift
    helm template test "${chart_dir}" --set tenant=team "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

document() {
    awk -v want="kind: $2" 'BEGIN{RS="\n---\n"} $0 ~ ("(^|\n)" want "(\n|$)") {print}' "$1"
}

rendered="$(render default)"

# --- 1. The existing hook Job still renders on every install/upgrade ---
migrate_job="$(document "${rendered}" Job)"
[ -n "${migrate_job}" ] || fail "the migrate Job must render"
printf '%s\n' "${migrate_job}" | grep -q 'name: team-backend-db-migrate$' || fail "the migrate Job must be tenant-scoped"
printf '%s\n' "${migrate_job}" | grep -q '"helm.sh/hook": post-install,post-upgrade' ||
    fail "the migrate Job must be a post-install,post-upgrade hook"
printf '%s\n' "${migrate_job}" | grep -q 'ERUN_DATABASE_URL' || fail "the migrate Job must carry the database URL"

# --- 2. The repair CronJob renders unconditionally, on a short interval, and
#        runs the identical image/command shape as the hook Job -- so a
#        schema-less database is repaired without anyone running Helm ---
cronjob="$(document "${rendered}" CronJob)"
[ -n "${cronjob}" ] || fail "the migrate-repair CronJob must render"
printf '%s\n' "${cronjob}" | grep -q 'name: team-backend-db-migrate-repair' || fail "the repair CronJob must be tenant-scoped"
printf '%s\n' "${cronjob}" | grep -q 'schedule: "\*/5 \* \* \* \*"' || fail "the repair CronJob must run on a short interval"
printf '%s\n' "${cronjob}" | grep -q 'concurrencyPolicy: Forbid' || fail "overlapping repair runs must be forbidden"
printf '%s\n' "${cronjob}" | grep -q 'ERUN_DATABASE_URL' || fail "the repair CronJob must carry the database URL"
printf '%s\n' "${cronjob}" | grep -q 'helm.sh/hook' && fail "the repair CronJob must not be a Helm hook -- it must run independently of any Helm operation"

# --- 3. Both mechanisms honour the same image override ---
overridden="$(render overridden --set-string imageOverrides.erun-backend-db=ghcr.io/sophium/erun-backend-db:pinned)"
printf '%s\n' "$(document "${overridden}" Job)" | grep -q 'image: ghcr.io/sophium/erun-backend-db:pinned' ||
    fail "imageOverrides.erun-backend-db must override the migrate Job's image"
printf '%s\n' "$(document "${overridden}" CronJob)" | grep -q 'image: ghcr.io/sophium/erun-backend-db:pinned' ||
    fail "imageOverrides.erun-backend-db must override the repair CronJob's image"

echo "OK"
