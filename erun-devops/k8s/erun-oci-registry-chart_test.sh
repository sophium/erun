#!/bin/sh

# Tests for the erun-oci-registry chart: the hosted registry (zot) is a
# platform singleton whose auth model is delegated entirely to
# erun-backend-api's token service, so what matters here is that the chart
# never invents its own trust — the realm and the trusted signing-key Secret
# are both operator-supplied and required, never generated — and that the
# retention window (a destructive default, per root AGENTS.md) is visible and
# tenant-configurable rather than hardcoded.
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh:
# helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-oci-registry"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-oci-registry-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

render() {
    out="${work_root}/render.yaml"
    helm template test "${chart_dir}" \
        --set tenant=team \
        --set-string registry.tokenRealm=https://api.frs-prod.services.erunpaas.com/v2/token \
        --set-string registry.signingKeySecretName=team-oci-registry-signing-key \
        "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

document() {
    awk -v want="kind: $2" 'BEGIN{RS="\n---\n"} $0 ~ ("(^|\n)" want "(\n|$)") {print}' "$1"
}

rendered="$(render)"
deployment="$(document "${rendered}" Deployment)"
configmap="$(document "${rendered}" ConfigMap)"
service="$(document "${rendered}" Service)"

# --- 1. Resources are tenant-scoped, never hardcoded to erun- ---
grep -q 'name: team-oci-registry$' "${rendered}" || fail "the Service must be named after the tenant"
grep -q 'name: team-oci-registry-data' "${rendered}" || fail "the PVC must be named after the tenant"
grep -q 'name: team-oci-registry-config' "${rendered}" || fail "the ConfigMap must be named after the tenant"

# --- 2. The token realm and service are the operator's, rendered into config.json ---
printf '%s\n' "${configmap}" | grep -q 'https://api.frs-prod.services.erunpaas.com/v2/token' ||
    fail "the rendered zot config must carry the configured token realm"
printf '%s\n' "${configmap}" | grep -q '"service": "registry.erunpaas.com"' ||
    fail "the rendered zot config must default the token service to registry.erunpaas.com"
printf '%s\n' "${configmap}" | grep -q '/etc/erun/oci-registry/signing-key/public.pem' ||
    fail "the rendered zot config must point cert at the mounted signing-key path"

# --- 3. The retention window is visible and tenant-configurable, not hardcoded ---
printf '%s\n' "${configmap}" | grep -q '"pulledWithin": "720h"' ||
    fail "the default retention window must be 30 days (720h)"
custom_configmap="$(document "$(render --set-string registry.retentionDays=7)" ConfigMap)"
printf '%s\n' "${custom_configmap}" | grep -q '"pulledWithin": "168h"' ||
    fail "registry.retentionDays must flow into the rendered retention policy"

# --- 4. The signing key is mounted read-only from the operator-named Secret,
#         never generated or embedded by the chart ---
printf '%s\n' "${deployment}" | grep -A2 'name: signing-key' | grep -q 'secretName: team-oci-registry-signing-key' ||
    fail "the signing-key volume must reference the operator-named Secret"
grep -q 'BEGIN PUBLIC KEY\|BEGIN CERTIFICATE' "${rendered}" &&
    fail "the chart must never embed key material of its own"

# --- 5. A missing token realm fails the render, loudly, before anything applies ---
if helm template test "${chart_dir}" \
    --set tenant=team \
    --set-string registry.signingKeySecretName=team-oci-registry-signing-key >"${work_root}/norealm.log" 2>&1; then
    fail "a render with no token realm must fail"
fi
grep -q 'registry.tokenRealm is required' "${work_root}/norealm.log" ||
    fail "the failure must name the missing token realm"

# --- 6. A missing signing-key Secret name fails the render too ---
if helm template test "${chart_dir}" \
    --set tenant=team \
    --set-string registry.tokenRealm=https://api.frs-prod.services.erunpaas.com/v2/token >"${work_root}/nokey.log" 2>&1; then
    fail "a render with no signing-key Secret name must fail"
fi
grep -q 'registry.signingKeySecretName is required' "${work_root}/nokey.log" ||
    fail "the failure must name the missing signing-key Secret"

# --- 7. The image is overridable, like every other wrapped component ---
overridden="$(render --set-string imageOverrides.erun-oci-registry=ghcr.io/sophium/erun-oci-registry:pinned)"
printf '%s\n' "$(document "${overridden}" Deployment)" | grep -q 'image: ghcr.io/sophium/erun-oci-registry:pinned' ||
    fail "imageOverrides.erun-oci-registry must override the rendered image"

# --- 8. The Service targets the registry's own port ---
printf '%s\n' "${service}" | grep -q 'port: 5000' || fail "the Service must expose port 5000"
printf '%s\n' "${service}" | grep -q 'targetPort: registry' || fail "the Service must target the named registry port"

echo "OK: erun-oci-registry chart"
