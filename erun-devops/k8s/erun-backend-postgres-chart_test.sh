#!/bin/sh

# Locks the postgres-restart-destroys-database fix at the chart level: the
# Deployment's pod template must never carry destructive logic, because that
# pod spec is exactly what a crash, eviction, node reboot, or `rollout
# restart` re-applies -- so anything destructive there re-runs on every one
# of those, not just on a deploy. The reset (when api.postgres.reset is set,
# which erun-common/deploy.go only does for a snapshot version) must instead
# ride as a Helm hook Job (reset-job.yaml) that runs once per install/upgrade
# and touches nothing in the running pod.
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh:
# helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-backend-postgres"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-backend-postgres-chart-test)"
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

# --- 1. Default (no reset): no reset Job at all, and the Deployment carries
#        no initContainers or destructive command of any kind ---
default_rendered="$(render default)"
document "${default_rendered}" Job | grep -q . && fail "no reset Job should render when api.postgres.reset is unset"
default_deployment="$(document "${default_rendered}" Deployment)"
printf '%s\n' "${default_deployment}" | grep -q 'initContainers' && fail "the Deployment must never carry an initContainers block"
printf '%s\n' "${default_rendered}" | grep -q 'rm -rf' && fail "the rendered manifests must never contain a filesystem wipe"

# --- 2. api.postgres.reset=true: a hook Job renders, scoped to install/upgrade,
#        and the Deployment pod template is byte-for-byte identical to the
#        no-reset render -- the property that makes a pod restart safe
#        regardless of how the chart was last deployed ---
reset_rendered="$(render reset --set api.postgres.reset=true)"
reset_job="$(document "${reset_rendered}" Job)"
[ -n "${reset_job}" ] || fail "a reset Job must render when api.postgres.reset=true"
printf '%s\n' "${reset_job}" | grep -q 'name: team-postgres-reset' || fail "the reset Job must be tenant-scoped"
printf '%s\n' "${reset_job}" | grep -q '"helm.sh/hook": post-install,post-upgrade' ||
    fail "the reset Job must be a post-install,post-upgrade hook, not a step in the Deployment's own lifecycle"
printf '%s\n' "${reset_job}" | grep -q '"helm.sh/hook-delete-policy": before-hook-creation' ||
    fail "the reset Job must replace its prior run, matching the migrate Job's hook-delete-policy"
printf '%s\n' "${reset_job}" | grep -q 'DROP DATABASE' || fail "the reset Job must drop the disposable database"
printf '%s\n' "${reset_job}" | grep -q 'CREATE DATABASE' || fail "the reset Job must recreate the database after dropping it"
printf '%s\n' "${reset_job}" | grep -q 'rm -rf' && fail "the reset must wipe over the network, not touch PGDATA on disk"

reset_deployment="$(document "${reset_rendered}" Deployment)"
[ "${default_deployment}" = "${reset_deployment}" ] ||
    fail "the Deployment must be byte-for-byte unchanged whether or not api.postgres.reset is set"

# --- 3. The reset Job honours the same image override as the Deployment ---
overridden="$(render overridden --set api.postgres.reset=true --set-string imageOverrides.erun-backend-postgres=ghcr.io/sophium/erun-backend-postgres:pinned)"
printf '%s\n' "$(document "${overridden}" Job)" | grep -q 'image: ghcr.io/sophium/erun-backend-postgres:pinned' ||
    fail "imageOverrides.erun-backend-postgres must override the reset Job's image"

echo "OK"
