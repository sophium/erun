#!/bin/sh

# Tests for the erun-zitadel chart's topology: Zitadel v4 core serves no
# interactive login, so a render that carries core alone is broken by
# construction. What is locked here is what makes the component work at all —
# both containers in one pod, the login-client PAT handed over through the
# shared bootstrap volume, Login V2 explicitly enabled against the external
# origin, one Ingress splitting /ui/v2/login from everything else — plus the
# masterkey posture: operator-supplied, never generated, never in argv.
#
# Lives beside the chart rather than inside it, like erun-devops-chart_test.sh:
# helm renders every file under templates/.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-zitadel"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-zitadel-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

render() {
    out="${work_root}/render.yaml"
    helm template test "${chart_dir}" \
        --set tenant=team \
        --set-string platform.authHost=auth.example.test \
        --set-string zitadel.masterkeySecretName=team-zitadel-masterkey \
        "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

# The pod's containers: section, with initContainers and volumes excluded so the
# postgres wait never counts as an application container.
containers_section() {
    awk '/^      containers:/{inside=1;next} /^      volumes:/{inside=0} inside' "$1"
}

container_names() {
    containers_section "$1" | sed -n 's/^        - name: \(.*\)$/\1/p'
}

# One container entry as its own block, so an assertion cannot pass by matching
# a line that belongs to the sibling container.
container() {
    containers_section "$1" | awk -v want="        - name: $2" '
        $0 == want {inside=1;print;next}
        /^        - name: /{inside=0}
        inside {print}
    '
}

# One rendered manifest of the given kind, so an Ingress assertion cannot pass
# by matching a line in the Service or the Deployment.
document() {
    awk -v want="kind: $2" 'BEGIN{RS="\n---\n"} $0 ~ ("(^|\n)" want "(\n|$)") {print}' "$1"
}

# The pod's initContainers: section only, so an assertion about restore-pats
# cannot pass by matching a line that belongs to a regular container.
init_containers_section() {
    awk '/^      initContainers:/{inside=1;next} /^      containers:/{inside=0} inside' "$1"
}

# One initContainer entry as its own block.
init_container() {
    init_containers_section "$1" | awk -v want="        - name: $2" '
        $0 == want {inside=1;print;next}
        /^        - name: /{inside=0}
        inside {print}
    '
}

rendered=$(render)

# --- 1. Three containers: core has no login UI, and a sidecar bootstraps the
#        OIDC applications the console/CLI need (#605) ---
names="${work_root}/containers.txt"
container_names "${rendered}" >"${names}"
grep -qx 'erun-zitadel' "${names}" || fail "the pod must run Zitadel core"
grep -qx 'erun-zitadel-login' "${names}" ||
    fail "the pod must run the separate Login V2 container; core alone answers Not Found at /ui/v2/login"
grep -qx 'oidc-bootstrap' "${names}" ||
    fail "the pod must run the OIDC application bootstrap sidecar"
[ "$(wc -l <"${names}" | tr -d ' ')" = "3" ] ||
    fail "the IdP pod is exactly core + login + oidc-bootstrap; anything else is a topology change"

# --- 2. The PAT handoff: one volume, written by core, read by login ---
core="${work_root}/core.yaml"
login="${work_root}/login.yaml"
container "${rendered}" erun-zitadel >"${core}"
container "${rendered}" erun-zitadel-login >"${login}"
grep -q 'value: "/zitadel/bootstrap/login-client.pat"' "${core}" ||
    fail "core must write the IAM_LOGIN_CLIENT PAT into the shared bootstrap dir"
grep -q 'ZITADEL_SERVICE_USER_TOKEN_FILE' "${login}" ||
    fail "the login container authenticates with the login-client PAT"
grep -q 'value: "/zitadel/bootstrap/login-client.pat"' "${login}" ||
    fail "the login container must read the very file core writes"
grep -q '^              mountPath: /zitadel/bootstrap$' "${core}" ||
    fail "core must mount the shared bootstrap volume"
grep -q '^              mountPath: /zitadel/bootstrap$' "${login}" ||
    fail "the login container must mount the shared bootstrap volume"
grep -q '^        - name: bootstrap$' "${rendered}" ||
    fail "the bootstrap volume the handoff depends on must render"

# --- 2b. Both PATs carry an expiration, because that is what mints them ---
# Verified against v4.15.3: with no expiration date core creates the two machine
# users and writes no token at all, so the login container waits on a file that
# never appears and the pod never goes Ready. An absent or empty value here is
# that outage, not a relaxed policy.
for var in ZITADEL_FIRSTINSTANCE_ORG_MACHINE_PAT_EXPIRATIONDATE \
    ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_PAT_EXPIRATIONDATE; do
    grep -A1 "name: ${var}" "${core}" | grep -qE 'value: "[0-9]{4}-' ||
        fail "${var} must render a date; without one core mints no PAT and the login container never starts"
done

# --- 3. The masterkey is the operator's, and never reaches argv ---
grep -q 'masterkeyFromEnv' "${core}" ||
    fail "the masterkey must be read from the environment, not passed on the command line"
grep -q -- '--masterkey$' "${core}" &&
    fail "the masterkey must never appear in argv"
grep -A3 'name: ZITADEL_MASTERKEY' "${core}" | grep -q 'name: team-zitadel-masterkey' ||
    fail "the masterkey must come from the operator-named Secret"
grep -q 'kind: Secret' "${rendered}" || fail "the chart should still render its own admin Secret"
grep -q 'masterkey:' "${rendered}" &&
    fail "the chart must never render a masterkey value of its own"

# --- 4. A missing masterkey fails the render, loudly, before anything applies ---
if helm template test "${chart_dir}" \
    --set-string platform.authHost=auth.example.test >"${work_root}/nokey.log" 2>&1; then
    fail "a render with no masterkey Secret named must fail"
fi
grep -q 'masterkeySecretName is required' "${work_root}/nokey.log" ||
    fail "the failure must name the missing value"

# --- 5. A missing external domain fails the render too ---
if helm template test "${chart_dir}" \
    --set-string zitadel.masterkeySecretName=k >"${work_root}/nohost.log" 2>&1; then
    fail "a render with no external domain must fail"
fi
grep -q 'external domain is required' "${work_root}/nohost.log" ||
    fail "the failure must name the missing external domain"

# --- 6. Login V2 is switched on explicitly, against the external origin ---
grep -q 'name: ZITADEL_DEFAULTINSTANCE_FEATURES_LOGINV2_REQUIRED' "${core}" ||
    fail "Login V2 must be required, or authorize renders nothing"
grep -q 'value: "https://auth.example.test/ui/v2/login/"' "${core}" ||
    fail "the Login V2 base URI must name the external origin"
grep -q 'value: "https://auth.example.test/ui/v2/login/login?authRequest="' "${core}" ||
    fail "the OIDC login URL must point at Login V2"
grep -q 'value: "https://auth.example.test/ui/v2/login/logout?post_logout_redirect="' "${core}" ||
    fail "the OIDC logout URL must point at Login V2"

# --- 7. ZITADEL_EXTERNAL* is the reachable origin, not a cluster address ---
# Discovery hands these endpoints to the browser, so a Service name here is a
# silently broken sign-in rather than a rendering detail.
grep -A1 'name: ZITADEL_EXTERNALDOMAIN' "${core}" | grep -q 'value: "auth.example.test"' ||
    fail "the external domain must be the public host"
grep -A1 'name: ZITADEL_EXTERNALSECURE' "${core}" | grep -q 'value: "true"' ||
    fail "an ingress-terminated origin is secure as far as discovery is concerned"
grep -A1 'name: ZITADEL_EXTERNALDOMAIN' "${core}" | grep -q 'team-zitadel' &&
    fail "the external domain must never be the in-cluster Service name"

# --- 8. One origin, two Ingress paths ---
ingress="${work_root}/ingress.yaml"
document "${rendered}" Ingress >"${ingress}"
[ -s "${ingress}" ] || fail "the component must render an Ingress"
grep -q '^  ingressClassName: traefik$' "${ingress}" ||
    fail "the default ingress class should match the cluster edge's controller"
grep -q '^          - path: /ui/v2/login$' "${ingress}" ||
    fail "the login path must route to the login container"
grep -q '^          - path: /$' "${ingress}" ||
    fail "everything else — discovery, authorize, token, jwks — must route to core"
awk '/- path: \/ui\/v2\/login$/{want=1} want && /port:/{getline; print; exit}' "${ingress}" |
    grep -q 'name: login' || fail "/ui/v2/login must reach the login container's port"
awk '/- path: \/$/{want=1} want && /port:/{getline; print; exit}' "${ingress}" |
    grep -q 'name: http' || fail "/ must reach core's port"
grep -q '^      secretName: team-zitadel-tls$' "${ingress}" ||
    fail "a secure origin must reference its TLS Secret"

# --- 9. The login container's wait for core is legible ---
# It restarts until core writes the PAT; the probe is what turns that into a
# reported wait instead of a bare CrashLoopBackOff.
grep -q 'path: /ui/v2/login/healthy' "${login}" ||
    fail "the login container needs a startup probe on its own health endpoint"

# --- 10. Tenant scoping, like every other component chart ---
grep -q '^  name: team-zitadel$' "${rendered}" ||
    fail "resources must be named after the tenant, never a hardcoded erun- prefix"
grep -q 'name: erun-zitadel$' "${ingress}" &&
    fail "the Ingress must be tenant-scoped"

# --- 11. Storage is the shared postgres instance, in its own database ---
grep -A1 'name: ZITADEL_DATABASE_POSTGRES_HOST' "${core}" | grep -q 'value: "team-postgres"' ||
    fail "the IdP stores its data on the tenant's shared postgres"
grep -A1 'name: ZITADEL_DATABASE_POSTGRES_DATABASE' "${core}" | grep -q 'value: "zitadel"' ||
    fail "the IdP must own its own database on that instance"

# --- 12. The images ride imageOverrides, and both move together ---
rendered=$(render \
    --set-string imageOverrides.erun-zitadel=reg.test/erun-zitadel:v9.9.9 \
    --set-string imageOverrides.erun-zitadel-login=reg.test/erun-zitadel-login:v9.9.9)
container "${rendered}" erun-zitadel | grep -q 'image: reg.test/erun-zitadel:v9.9.9' ||
    fail "core's image must be overridable"
container "${rendered}" erun-zitadel-login | grep -q 'image: reg.test/erun-zitadel-login:v9.9.9' ||
    fail "the login image must be overridable"

# --- 13. An explicit insecure origin is honored ---
# `default true` silently re-enables a boolean an operator turned off, so the
# externalSecure=false path has to be exercised rather than assumed.
rendered=$(render --set zitadel.externalSecure=false --set zitadel.externalPort=80)
container "${rendered}" erun-zitadel | grep -A1 'name: ZITADEL_EXTERNALSECURE' | grep -q 'value: "false"' ||
    fail "an explicit insecure origin must be honored"
container "${rendered}" erun-zitadel | grep -q 'value: "http://auth.example.test/ui/v2/login/"' ||
    fail "the Login V2 base URI must follow the scheme of the origin"
document "${rendered}" Ingress | grep -q 'secretName:' &&
    fail "an insecure origin has no TLS Secret to reference"

# --- 14. The OIDC bootstrap sidecar: present, shares the PAT volume, uses the
#         dedicated ServiceAccount, and knows the configured ConfigMap ---
# The reconcile logic itself (project/app resolution, JWT token types, grant
# types, the ConfigMap publish guard) lives in the baked erun-oidc-bootstrap
# script (erun-devops/docker/erun-devops/oidc-bootstrap.sh) and is locked by
# its own oidc-bootstrap_test.sh, not by grepping this chart's render.
rendered=$(render)
bootstrap="${work_root}/oidc-bootstrap.yaml"
container "${rendered}" oidc-bootstrap >"${bootstrap}"
[ -s "${bootstrap}" ] || fail "the pod must run the OIDC application bootstrap sidecar"
grep -q '^              mountPath: /zitadel/bootstrap$' "${bootstrap}" ||
    fail "the OIDC bootstrap sidecar must mount the shared bootstrap volume to read the org-owner PAT"
grep -q -- '- erun-oidc-bootstrap$' "${bootstrap}" ||
    fail "the sidecar must run the baked erun-oidc-bootstrap script"
grep -q 'name: CONFIGMAP_NAME' "${bootstrap}" ||
    fail "the OIDC bootstrap sidecar must know which ConfigMap to publish client ids to"
grep -A1 'name: CONFIGMAP_NAME' "${bootstrap}" | grep -q 'value: "team-zitadel-oidc-clients"' ||
    fail "the ConfigMap must default to <tenant>-zitadel-oidc-clients"

# --- 15. The bootstrap sidecar has its own least-privilege ServiceAccount ---
sa="${work_root}/oidc-sa.yaml"
document "${rendered}" ServiceAccount >"${sa}"
grep -q '^  name: team-zitadel$' "${sa}" ||
    fail "the tenant-scoped ServiceAccount for the OIDC bootstrap sidecar must render"
grep -q 'serviceAccountName: team-zitadel$' "${rendered}" ||
    fail "the pod must run as the dedicated ServiceAccount, not the namespace default"
role="${work_root}/oidc-role.yaml"
document "${rendered}" Role >"${role}"
grep -q 'resources: \["configmaps"\]' "${role}" ||
    fail "the bootstrap ServiceAccount's Role must be scoped to configmaps only"
grep -q 'verbs: \["get", "create", "update", "patch"\]' "${role}" ||
    fail "the bootstrap Role must not carry delete or list-everything verbs it does not need"

# --- 16. The bootstrap sidecar's image rides imageOverrides too ---
rendered=$(render --set-string imageOverrides.erun-devops=reg.test/erun-devops:v9.9.9)
container "${rendered}" oidc-bootstrap | grep -q 'image: reg.test/erun-devops:v9.9.9' ||
    fail "the OIDC bootstrap sidecar's image must be overridable via imageOverrides.erun-devops"

# --- 17. The console app's redirect URI(s) follow platform.consoleUrl and
#         zitadel.oidc.additionalConsoleRedirectUris, registered alongside it
#         rather than replacing it (#1131) ---
rendered=$(render --set-string platform.consoleUrl=https://console.example.test)
container "${rendered}" oidc-bootstrap | grep -q 'value: "\[\\"https://console.example.test\\"\]"' ||
    fail "the console app's redirect URI must default to platform.consoleUrl"

rendered=$(render \
    --set-string platform.consoleUrl=https://console.example.test \
    --set-string 'zitadel.oidc.additionalConsoleRedirectUris[0]=https://console2.example.test')
container "${rendered}" oidc-bootstrap | grep -q 'https://console.example.test\\",\\"https://console2.example.test' ||
    fail "additionalConsoleRedirectUris must register alongside consoleRedirectUri, not replace it"

# --- 17a. platform.baseDomain also registers the bare apex and "www."
#          hosts as console redirect URIs -- the erun-console chart
#          301-redirects those same two hosts to the canonical console
#          origin by default, but that redirect lives in a separate Helm
#          release this chart cannot see, so a visitor who ever reaches the
#          console app directly on either host must still be able to sign
#          in there instead of hitting an unregistered-redirect-uri dead
#          end ---
rendered=$(render \
    --set-string platform.consoleUrl=https://console.example.test \
    --set-string platform.baseDomain=example.test)
container "${rendered}" oidc-bootstrap | grep -q 'https://console.example.test\\",\\"https://example.test/\\",\\"https://www.example.test/' ||
    fail "platform.baseDomain must register https://<baseDomain>/ and https://www.<baseDomain>/ as console redirect URIs, alongside platform.consoleUrl"

rendered=$(render --set-string platform.baseDomain=example.test)
container "${rendered}" oidc-bootstrap | grep -q 'value: "\[\\"https://example.test/\\",\\"https://www.example.test/\\"\]"' ||
    fail "the apex/www redirect URIs must register even when platform.consoleUrl is unset"

# --- 17b. An operator can opt out, for a platform whose apex/www genuinely
#          serve something other than the console ---
rendered=$(render \
    --set-string platform.consoleUrl=https://console.example.test \
    --set-string platform.baseDomain=example.test \
    --set zitadel.oidc.baseDomainRedirectUrisEnabled=false)
container "${rendered}" oidc-bootstrap | grep -q 'value: "\[\\"https://console.example.test\\"\]"' ||
    fail "zitadel.oidc.baseDomainRedirectUrisEnabled=false must suppress the apex/www redirect URIs"

# --- 17c. The mobile app's redirect URI has no default (#1105): a mobile
#          client's custom URL scheme belongs to whatever app actually ships,
#          so this platform mints no erun-mobile client until an operator
#          names one via zitadel.oidc.mobileRedirectUris ---
rendered=$(render)
container "${rendered}" oidc-bootstrap | grep -q 'name: MOBILE_REDIRECT_URIS' ||
    fail "the OIDC bootstrap sidecar must know which mobile redirect URIs to reconcile"
container "${rendered}" oidc-bootstrap | grep -A1 'name: MOBILE_REDIRECT_URIS' | grep -q 'value: "\[\]"' ||
    fail "MOBILE_REDIRECT_URIS must default to an empty list, unlike the console's platform.consoleUrl default"

rendered=$(render --set-string 'zitadel.oidc.mobileRedirectUris[0]=erun://callback')
container "${rendered}" oidc-bootstrap | grep -A1 'name: MOBILE_REDIRECT_URIS' | grep -q 'value: "\[\\"erun://callback\\"\]"' ||
    fail "zitadel.oidc.mobileRedirectUris must configure MOBILE_REDIRECT_URIS"

# --- 18. The external domain, EXTERNAL_DOMAIN, is what the sidecar sends as
#         the Management API's Host header (see oidc-bootstrap_test.sh for the
#         header itself, which is the baked script's behavior) ---
rendered=$(render)
bootstrap="${work_root}/oidc-bootstrap.yaml"
container "${rendered}" oidc-bootstrap >"${bootstrap}"
grep -q 'name: EXTERNAL_DOMAIN' "${bootstrap}" ||
    fail "the OIDC bootstrap sidecar must know the external domain to send as Host"
grep -A1 'name: EXTERNAL_DOMAIN' "${bootstrap}" | grep -q 'value: "auth.example.test"' ||
    fail "EXTERNAL_DOMAIN must be the platform's external domain"

# --- 20. The bootstrap PATs survive a pod restart (#1047, sophium/erun#1047) ---
# Core writes both PATs only once, at first-instance init, into an emptyDir
# that does not survive a restart; without persistence a restarted pod's login
# and oidc-bootstrap containers wait forever on files that never reappear.
restore="${work_root}/restore-pats.yaml"
init_container "${rendered}" restore-pats >"${restore}"
[ -s "${restore}" ] || fail "the pod must run a restore-pats init container before core starts"
grep -q '^              mountPath: /zitadel/bootstrap$' "${restore}" ||
    fail "restore-pats must mount the shared bootstrap volume to seed it"
grep -q 'name: PATS_SECRET_NAME' "${restore}" ||
    fail "restore-pats must know which Secret durably holds the PATs"
grep -A1 'name: PATS_SECRET_NAME' "${restore}" | grep -q 'value: "team-zitadel-pats"' ||
    fail "the PATs Secret must default to <tenant>-zitadel-pats"
grep -q 'kubectl get secret' "${restore}" ||
    fail "restore-pats must read the durable PATs Secret"
grep -q 'first-instance init will mint the PATs' "${restore}" ||
    fail "restore-pats must no-op (not fail) when the Secret does not exist yet, i.e. on true first init"

grep -q 'name: PATS_SECRET_NAME' "${bootstrap}" ||
    fail "the OIDC bootstrap sidecar must know which Secret to persist the PATs into"
grep -A1 'name: PATS_SECRET_NAME' "${bootstrap}" | grep -q 'value: "team-zitadel-pats"' ||
    fail "the sidecar's PATs Secret name must match restore-pats'"
# Persisting the PATs into that Secret is the baked script's behavior, locked
# by oidc-bootstrap_test.sh, not by grepping this chart's render.

# --- 21. Least-privilege RBAC for the durable PATs Secret ---
role="${work_root}/pat-persist-role.yaml"
document "${rendered}" Role | awk '/name: team-zitadel-pat-persist/{f=1} f{print} f && /^---$/{exit}' >"${role}"
[ -s "${role}" ] || fail "a dedicated Role for the PATs Secret must render"
grep -q 'resourceNames: \["team-zitadel-pats"\]' "${role}" ||
    fail "get/update/patch on the PATs Secret must be scoped to its own name"
grep -q 'verbs: \["create"\]' "${role}" ||
    fail "create cannot be name-scoped (the object does not exist yet) so it must stay a separate rule"

# --- 22. The dedicated ServiceAccount carries the env's image-pull credentials ---
# The pod runs as team-zitadel rather than the namespace default, so it stops
# inheriting whatever registry secret default carries; the private core/login
# images (and the oidc-bootstrap sidecar's erun-devops image) never pull unless
# this ServiceAccount names the same credential explicitly.
rendered=$(render)
sa="${work_root}/sa-no-secret.yaml"
document "${rendered}" ServiceAccount >"${sa}"
grep -q 'imagePullSecrets' "${sa}" &&
    fail "no imagePullSecrets should render when none are configured"

rendered=$(render --set-string 'imagePullSecrets[0].name=ghcr-pull')
document "${rendered}" ServiceAccount >"${sa}"
grep -q '^imagePullSecrets:$' "${sa}" ||
    fail "a configured image pull secret should render on the dedicated ServiceAccount"
grep -q '^  - name: ghcr-pull$' "${sa}" ||
    fail "the ServiceAccount's imagePullSecrets should name the configured secret"

echo "PASS: erun-zitadel chart topology"
