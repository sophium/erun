#!/bin/sh

# Locks the erun-docs chart's dedicated ServiceAccount: the deploy Job runs as
# team-docs-deployer rather than the namespace default, so it stops inheriting
# whatever registry secret default carries, and the private erun-docs image
# never pulls unless this ServiceAccount names the same credential explicitly
# (sophium/erun#1052).
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh:
# helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-docs"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-docs-chart-test)"
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
        --set docs.enabled=true \
        --set-string docs.accountId=cf-account \
        "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

# One rendered manifest of the given kind, so an assertion cannot pass by
# matching a line that belongs to the Job instead of the ServiceAccount.
document() {
    awk -v want="kind: $2" 'BEGIN{RS="\n---\n"} $0 ~ ("(^|\n)" want "(\n|$)") {print}' "$1"
}

# --- 1. The deploy Job runs as the dedicated ServiceAccount ---
rendered=$(render)
grep -q '^      serviceAccountName: team-docs-deployer$' "${rendered}" ||
    fail "the deploy Job must run as the dedicated docs-deployer ServiceAccount"

# --- 2. No imagePullSecrets render when none are configured ---
sa="${work_root}/sa.yaml"
document "${rendered}" ServiceAccount >"${sa}"
grep -q 'imagePullSecrets' "${sa}" &&
    fail "no imagePullSecrets should render on the ServiceAccount when none are configured"

# --- 3. A configured image pull secret reaches the dedicated ServiceAccount ---
rendered=$(render --set-string 'imagePullSecrets[0].name=ghcr-pull')
document "${rendered}" ServiceAccount >"${sa}"
grep -q '^imagePullSecrets:$' "${sa}" ||
    fail "a configured image pull secret should render on the docs-deployer ServiceAccount"
grep -q '^  - name: ghcr-pull$' "${sa}" ||
    fail "the ServiceAccount's imagePullSecrets should name the configured secret"

echo "PASS: erun-docs chart ServiceAccount"
