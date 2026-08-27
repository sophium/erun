#!/bin/sh

# The platform's apex and www hostnames used to resolve to nothing, only
# console.<baseDomain> was served. Locks the apex/www redirect this chart now
# renders — a second Ingress + Traefik Middleware that 301s the apex and www
# hosts to the canonical console origin, on by default whenever
# platform.baseDomain is known, overridable off, and never touching the
# canonical Ingress's own rule (sign-in depends on staying one origin).
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh
# and erun-zitadel-chart_test.sh: helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-console"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-console-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# --namespace pins .Release.Namespace to a known value: the redirect
# Middleware's Traefik router reference is namespace-qualified.
render() {
    out="${work_root}/render.yaml"
    helm template test "${chart_dir}" \
        --namespace team-prod \
        --set tenant=team \
        "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

# One rendered manifest of the given kind + name, as its own block, so an
# assertion cannot pass by matching a line that belongs to a different object.
document() {
    awk -v want_kind="kind: $2" -v want_name="name: $3" '
        BEGIN{RS="\n---\n"}
        $0 ~ ("(^|\n)" want_kind "(\n|$)") && $0 ~ ("(^|\n)  " want_name "(\n|$)") {print}
    ' "$1"
}

# --- 1. No platform.baseDomain, no console.apexHost: the redirect defaults
#        off, and the chart renders exactly what it always did ---
rendered=$(render --set-string console.externalDomain=console.example.test)
grep -q 'kind: Middleware' "${rendered}" &&
    fail "no Middleware should render with no base domain and no explicit apex host"
grep -q 'console-apex-redirect' "${rendered}" &&
    fail "no apex-redirect Ingress should render with no base domain and no explicit apex host"
[ "$(grep -c '^kind: Ingress$' "${rendered}")" = "1" ] ||
    fail "exactly one Ingress (the canonical console host) should render by default"

# The disabled state must still be legible: an unset baseDomain switches the
# redirect off exactly like the case above, but it must not do so silently —
# a status ConfigMap always renders and says the redirect is off and why.
status="${work_root}/status-unset.yaml"
document "${rendered}" ConfigMap team-console-apex-status >"${status}"
[ -s "${status}" ] || fail "a status ConfigMap must render even when the redirect is off by default"
grep -q '^  enabled: "false"$' "${status}" ||
    fail "the status ConfigMap must record enabled: \"false\" when no base domain or apex host is configured"
grep -q 'no platform.baseDomain and no console.apexHost is configured' "${status}" ||
    fail "the status ConfigMap must name the reason the redirect is off — an unset baseDomain must never disable it in silence"

# --- 2. platform.baseDomain set: the redirect is on by default ---
rendered=$(render --set-string platform.baseDomain=erunpaas.com --set-string console.certManagerIssuer=erun-cloudflare)

mw="${work_root}/middleware.yaml"
document "${rendered}" Middleware team-console-apex-redirect >"${mw}"
[ -s "${mw}" ] || fail "a Middleware must render once platform.baseDomain is known"
grep -q '^apiVersion: traefik.io/v1alpha1$' "${mw}" ||
    fail "the redirect must use the Traefik v1alpha1 Middleware CRD, matching the traefik chart terraform-erun-cluster-edge installs"
grep -q 'redirectRegex:' "${mw}" ||
    fail "the redirect must be a redirectRegex Middleware so path and query survive"
grep -q 'replacement: "https://console.erunpaas.com\$1"' "${mw}" ||
    fail "the redirect target must default to this chart's own externalDomain when platform.consoleUrl is unset"
grep -q 'permanent: true' "${mw}" ||
    fail "the redirect must be a 301, not a temporary redirect"

redirect_ingress="${work_root}/redirect-ingress.yaml"
document "${rendered}" Ingress team-console-apex-redirect >"${redirect_ingress}"
[ -s "${redirect_ingress}" ] || fail "an apex-redirect Ingress must render once platform.baseDomain is known"
grep -q '^    traefik.ingress.kubernetes.io/router.middlewares: team-prod-team-console-apex-redirect@kubernetescrd$' "${redirect_ingress}" ||
    fail "the redirect Ingress must attach the Middleware via the namespace-qualified Traefik annotation"
grep -q '^    - host: erunpaas.com$' "${redirect_ingress}" ||
    fail "the redirect Ingress must route the bare apex host"
grep -q '^    - host: www.erunpaas.com$' "${redirect_ingress}" ||
    fail "the redirect Ingress must route the www host"
grep -q 'cert-manager.io/issuer' "${redirect_ingress}" &&
    fail "the redirect Ingress must not carry its own cert-manager annotation — a second Certificate for the same Secret races the canonical Ingress's"

canonical_ingress="${work_root}/canonical-ingress.yaml"
document "${rendered}" Ingress team-console >"${canonical_ingress}"
[ -s "${canonical_ingress}" ] || fail "the canonical console Ingress must still render"
grep -q '^    - host: console.erunpaas.com$' "${canonical_ingress}" ||
    fail "the canonical Ingress must still route only the console host"
[ "$(grep -c '^    - host:' "${canonical_ingress}")" = "1" ] ||
    fail "the canonical Ingress's rules must stay exactly one host — the apex/www hosts belong on the redirect Ingress, never here"
grep -q 'router.middlewares' "${canonical_ingress}" &&
    fail "the redirect Middleware must never attach to the canonical Ingress; its rule is the only one allowed to reach the Service"

status="${work_root}/status-enabled.yaml"
document "${rendered}" ConfigMap team-console-apex-status >"${status}"
[ -s "${status}" ] || fail "a status ConfigMap must render when the redirect is enabled"
grep -q '^  enabled: "true"$' "${status}" ||
    fail "the status ConfigMap must record enabled: \"true\" once platform.baseDomain is known"
grep -q '^  apexHost: "erunpaas.com"$' "${status}" ||
    fail "the status ConfigMap must record the resolved apex host"
grep -q '^  wwwHost: "www.erunpaas.com"$' "${status}" ||
    fail "the status ConfigMap must record the resolved www host"

# --- 3. The shared certificate covers all three hosts, so the redirect
#        Ingress's TLS terminates before Traefik would otherwise present the
#        controller's self-signed default ---
grep -A4 '^  tls:' "${canonical_ingress}" | grep -q 'console.erunpaas.com' &&
    grep -A4 '^  tls:' "${canonical_ingress}" | grep -q '      - erunpaas.com$' &&
    grep -A4 '^  tls:' "${canonical_ingress}" | grep -q 'www.erunpaas.com' ||
    fail "the canonical Ingress's Certificate request must cover apex and www as SANs too"
grep -A2 '^  tls:' "${redirect_ingress}" | grep -q 'secretName: team-console-tls' ||
    fail "the redirect Ingress must reference the same TLS Secret the canonical Ingress's Certificate fills"
grep -q 'hosts:' "${redirect_ingress}" &&
    fail "the redirect Ingress must not re-request its own Certificate by listing tls hosts"

# --- 4. platform.consoleUrl, when set, is the redirect target — not this
#        chart's own externalDomain — matching GET /v1/platform's contract ---
rendered=$(render --set-string platform.baseDomain=erunpaas.com --set-string platform.consoleUrl=https://console.other-instance.test)
document "${rendered}" Middleware team-console-apex-redirect | grep -q 'replacement: "https://console.other-instance.test\$1"' ||
    fail "platform.consoleUrl must win over externalDomain as the redirect target when both are set"

# --- 5. An operator whose apex serves something else can turn the whole
#        thing off, and the console host keeps working exactly as before ---
rendered=$(render --set-string platform.baseDomain=erunpaas.com --set console.apexRedirectEnabled=false)
grep -q 'kind: Middleware' "${rendered}" &&
    fail "console.apexRedirectEnabled=false must render no Middleware"
grep -q 'console-apex-redirect' "${rendered}" &&
    fail "console.apexRedirectEnabled=false must render no apex-redirect Ingress"
document "${rendered}" Ingress team-console | grep -q '^    - host: console.erunpaas.com$' ||
    fail "the canonical console Ingress must still work when the apex redirect is disabled"

# An explicit opt-out gets its own reason, distinct from an unset baseDomain's
# — the same "false" outcome must not collapse two different causes into one
# sentence (root AGENTS.md's "Distinguish causes before writing copy").
status="${work_root}/status-explicit-off.yaml"
document "${rendered}" ConfigMap team-console-apex-status >"${status}"
[ -s "${status}" ] || fail "a status ConfigMap must render when the redirect is explicitly disabled"
grep -q '^  enabled: "false"$' "${status}" ||
    fail "the status ConfigMap must record enabled: \"false\" when console.apexRedirectEnabled=false"
grep -q 'console.apexRedirectEnabled is set to false' "${status}" ||
    fail "an explicit opt-out must be recorded with its own reason, not the unset-baseDomain reason"

# --- 6. console.apexHost / console.wwwHost override the derived defaults,
#        e.g. for an apex that differs from platform.baseDomain ---
rendered=$(render \
    --set-string platform.baseDomain=erunpaas.com \
    --set-string console.apexHost=apex.custom.test \
    --set-string console.wwwHost=www.custom.test)
redirect_ingress="${work_root}/custom-redirect-ingress.yaml"
document "${rendered}" Ingress team-console-apex-redirect >"${redirect_ingress}"
grep -q '^    - host: apex.custom.test$' "${redirect_ingress}" ||
    fail "console.apexHost must override the derived apex host"
grep -q '^    - host: www.custom.test$' "${redirect_ingress}" ||
    fail "console.wwwHost must override the derived www host"

# --- 7. A missing external domain still fails the render loudly, unaffected
#        by the apex-redirect wiring above ---
if helm template test "${chart_dir}" --namespace team-prod --set tenant=team >"${work_root}/nohost.log" 2>&1; then
    fail "a render with no external domain must fail"
fi
grep -q 'external domain is required' "${work_root}/nohost.log" ||
    fail "the failure must name the missing external domain"

echo "PASS: erun-console apex/www redirect"
