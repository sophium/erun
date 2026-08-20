#!/bin/sh

# Locks the DBOS system-database wiring for erun-backend-api (#1047): live env
# provisioning and the server-side release queue both require a non-nil
# DBOSContext (server.go's newEnvironmentProvisioner/newReleaseQueue), and
# DBOSContext is built only from the DBOS_SYSTEM_DATABASE_URL env var. Before
# this fix the chart never rendered it under any flag, so api.envDeployer.enabled
# could never actually enable live deploys -- POST /v1/environments/{id}/deploy
# always answered 501, no matter what was set.
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh
# and erun-zitadel-chart_test.sh: helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-backend-api"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-backend-api-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

render() {
    out="${work_root}/render.yaml"
    helm template test "${chart_dir}" \
        --set tenant=team \
        --set environment=prod \
        "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

container() {
    awk '/^      containers:/{inside=1} inside' "$1"
}

# The ServiceAccount manifest named $2, as its own block, so an assertion about
# one ServiceAccount cannot pass by matching a line that belongs to another.
service_account() {
    awk -v want="  name: $2" '
        BEGIN{RS="\n---\n"}
        $0 ~ /(^|\n)kind: ServiceAccount(\n|$)/ && $0 ~ ("(^|\n)" want "(\n|$)") {print}
    ' "$1"
}

# --- 1. Neither envDeployer nor the release queue is on: no DBOS wiring at all,
#        byte-for-byte unchanged from before #1047 ---
rendered=$(render)
container "${rendered}" | grep -q 'DBOS_SYSTEM_DATABASE_URL' &&
    fail "DBOS_SYSTEM_DATABASE_URL must not render when nothing needs DBOSContext"

# --- 2. api.envDeployer.enabled must actually enable live deploys (#1047) ---
rendered=$(render --set-string api.envDeployer.enabled=true)
core="${work_root}/api.yaml"
container "${rendered}" >"${core}"
grep -q 'name: DBOS_SYSTEM_DATABASE_URL' "${core}" ||
    fail "api.envDeployer.enabled must render DBOS_SYSTEM_DATABASE_URL, or DBOSContext stays nil and deploy always answers 501"
grep -A1 'name: DBOS_SYSTEM_DATABASE_URL' "${core}" | grep -q 'value: "postgres://erun:\$(ERUN_POSTGRES_PASSWORD)@team-postgres:5432/erun_dbos_sys?sslmode=disable"' ||
    fail "the DBOS database must be a separate database on the same postgres instance, not ERUN_DATABASE_URL's own database"
grep -q 'name: ERUN_ENV_DEPLOYER_SERVICE_ACCOUNT' "${core}" ||
    fail "envDeployer.enabled must still wire the deployer service account (unchanged by this fix)"

# --- 3. The release queue needs DBOSContext too (server.go's newReleaseQueue) ---
rendered=$(render --set-string api.releaseQueue.enabled=true --set-string api.releaseQueue.namespace=team-ux)
container "${rendered}" >"${core}"
grep -q 'name: DBOS_SYSTEM_DATABASE_URL' "${core}" ||
    fail "api.releaseQueue.enabled must also render DBOS_SYSTEM_DATABASE_URL, or the release queue never dispatches"

# --- 4. The DBOS database name and the full URL are overridable ---
rendered=$(render --set-string api.envDeployer.enabled=true --set-string api.dbos.database=custom_dbos)
container "${rendered}" >"${core}"
grep -A1 'name: DBOS_SYSTEM_DATABASE_URL' "${core}" | grep -q '/custom_dbos?' ||
    fail "api.dbos.database must override the DBOS database name"

rendered=$(render --set-string api.envDeployer.enabled=true --set-string api.dbos.databaseURL=postgres://custom/url)
container "${rendered}" >"${core}"
grep -A1 'name: DBOS_SYSTEM_DATABASE_URL' "${core}" | grep -q 'value: "postgres://custom/url"' ||
    fail "api.dbos.databaseURL must override the whole DBOS connection string"


# --- 5. The env-deployer ServiceAccount carries the env's image-pull
#        credentials, so the deploy Job it runs as can pull a private tenant
#        runtime image instead of relying on the namespace default SA ---
rendered=$(render --set-string api.envDeployer.enabled=true)
sa="${work_root}/sa-no-secret.yaml"
service_account "${rendered}" team-env-deployer >"${sa}"
[ -s "${sa}" ] || fail "the env-deployer ServiceAccount must render when envDeployer is enabled"
grep -q 'imagePullSecrets' "${sa}" &&
    fail "no imagePullSecrets should render when none are configured"

rendered=$(render --set-string api.envDeployer.enabled=true --set-string 'imagePullSecrets[0].name=ghcr-pull')
service_account "${rendered}" team-env-deployer >"${sa}"
grep -q '^imagePullSecrets:$' "${sa}" ||
    fail "a configured image pull secret should render on the env-deployer ServiceAccount"
grep -q '^  - name: ghcr-pull$' "${sa}" ||
    fail "the env-deployer ServiceAccount's imagePullSecrets should name the configured secret"

# --- 6. The API's own ServiceAccount gets the same credentials ---
service_account "${rendered}" team-api >"${sa}"
[ -s "${sa}" ] || fail "the API ServiceAccount must render when envDeployer is enabled"
grep -q '^imagePullSecrets:$' "${sa}" ||
    fail "a configured image pull secret should render on the API ServiceAccount too"

echo "PASS: erun-backend-api DBOS wiring"
