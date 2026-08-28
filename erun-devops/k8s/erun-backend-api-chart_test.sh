#!/bin/sh

# Locks the DBOS system-database wiring for erun-backend-api: live env
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

# --namespace pins .Release.Namespace to a known value: helm falls back to the
# ambient kubeconfig context's namespace otherwise, which makes any assertion
# about a chart-rendered release-namespace value (section 12 below) depend on
# whoever's machine runs this script.
render() {
    out="${work_root}/render.yaml"
    helm template test "${chart_dir}" \
        --namespace test \
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
#        byte-for-byte unchanged from before this wiring existed ---
rendered=$(render)
container "${rendered}" | grep -q 'DBOS_SYSTEM_DATABASE_URL' &&
    fail "DBOS_SYSTEM_DATABASE_URL must not render when nothing needs DBOSContext"

# --- 2. api.envDeployer.enabled must actually enable live deploys ---
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

# --- 7. The API also needs to *read* those secrets, not just pull with them:
#        its published-image probe interrogates the registry with the same
#        credential, which is the only way it can tell a tenant image that was
#        never published from one in a private namespace it may not read ---
container "${rendered}" >"${core}"
grep -q 'name: ERUN_ENV_DEPLOY_IMAGE_PULL_SECRETS' "${core}" ||
    fail "a configured image pull secret must be named to the API, or its published-image probe stays unauthenticated and the bootstrap path is unreachable"
grep -A1 'name: ERUN_ENV_DEPLOY_IMAGE_PULL_SECRETS' "${core}" | grep -q 'value: "ghcr-pull"' ||
    fail "ERUN_ENV_DEPLOY_IMAGE_PULL_SECRETS must carry the configured secret names"

role() {
    awk -v want="  name: $2" '
        BEGIN{RS="\n---\n"}
        $0 ~ /(^|\n)kind: Role(\n|$)/ && $0 ~ ("(^|\n)" want "(\n|$)") {print}
    ' "$1"
}

deployer_role="${work_root}/role.yaml"
role "${rendered}" team-api-env-deployer >"${deployer_role}"
[ -s "${deployer_role}" ] || fail "the env-deployer Role must render when envDeployer is enabled"
grep -q 'resources: \["secrets"\]' "${deployer_role}" ||
    fail "the API's Role must grant reading the pull secret, or the probe can never authenticate"
grep -q '      - "ghcr-pull"' "${deployer_role}" ||
    fail "the secrets grant must be scoped by name to the configured pull secrets"

rendered=$(render --set-string api.envDeployer.enabled=true)
container "${rendered}" | grep -q 'ERUN_ENV_DEPLOY_IMAGE_PULL_SECRETS' &&
    fail "no pull-secret env var should render when none are configured"
role "${rendered}" team-api-env-deployer >"${deployer_role}"
# The Role still grants secrets create/get/update unconditionally (#1112,
# placement credentials) even with no pull secret configured; what must NOT
# render without one is the resourceNames-scoped pull-secret sub-rule.
grep -q 'resourceNames' "${deployer_role}" &&
    fail "no resourceNames-scoped secrets grant should render when no pull secrets are configured"
grep -q 'resources: \["secrets"\]' "${deployer_role}" ||
    fail "the placement-credential secrets grant (#1112) must render regardless of pull-secret configuration"

# --- 8. Pin each Role the API's own Kubernetes client can run under against
#        what erun-backend-api's production Go source actually calls
#        (internal/jobexec, internal/provision), so a future call to a new
#        resource fails this local gate instead of surfacing as a live RBAC
#        denial only discovered by deploying (this is not the first
#        time a chart's Role fell out of step with the identity it
#        backs -- see erun-devops/AGENTS.md "Runtime Chart Rules") ---
go_internal="${script_dir}/../../erun-backend/erun-backend-api/internal"

go_source_matches() {
    find "${go_internal}" -name '*.go' ! -name '*_test.go' -print0 2>/dev/null |
        xargs -0 grep -F -l -- "$1" 2>/dev/null
}

require_role_resource() {
    # $1 = literal client-go call in erun-backend-api's own source
    # $2 = k8s resource string that call needs granted
    # $3 = rendered Role file to check it against
    if [ -n "$(go_source_matches "$1")" ]; then
        grep -q "\"$2\"" "$3" ||
            fail "erun-backend-api calls $1 (needs resource \"$2\"), but $3 does not grant it -- when code starts calling the Kubernetes API for a new resource, move the chart's Role in the same commit (see erun-devops/AGENTS.md)"
    fi
}

rendered=$(render --set-string api.envDeployer.enabled=true --set-string 'imagePullSecrets[0].name=ghcr-pull')
role "${rendered}" team-api-env-deployer >"${deployer_role}"
require_role_resource '.BatchV1().Jobs(' jobs "${deployer_role}"
require_role_resource '.CoreV1().Pods(' pods "${deployer_role}"
require_role_resource '.GetLogs(' 'pods/log' "${deployer_role}"
require_role_resource '.CoreV1().Secrets(' secrets "${deployer_role}"

# --- 8b. Pin the placement-credential secrets grant's verbs precisely
#         (#1112): deployexec.ensurePlacementSecret (called by this SA's own
#         process, not the deploy Job) creates then updates one
#         erun-ctx-cred-<contextId> Secret per remote-context deploy, and
#         reads its own write back on the AlreadyExists retry path -- never
#         list/watch/delete, which would let this identity enumerate or erase
#         secrets it has no reason to touch.
grep -q 'verbs: \["create", "get", "update"\]' "${deployer_role}" ||
    fail "the placement-credential secrets grant must be exactly create/get/update, no more and no less"

rendered=$(render --set-string api.releaseQueue.enabled=true --set-string api.releaseQueue.namespace=team-ux)
queue_role="${work_root}/queue-role.yaml"
role "${rendered}" team-api-release-queue >"${queue_role}"
require_role_resource '.BatchV1().Jobs(' jobs "${queue_role}"
require_role_resource '.CoreV1().Pods(' pods "${queue_role}"
require_role_resource '.GetLogs(' 'pods/log' "${queue_role}"

cluster_role() {
    awk -v want="  name: $2" '
        BEGIN{RS="\n---\n"}
        $0 ~ /(^|\n)kind: ClusterRole(\n|$)/ && $0 ~ ("(^|\n)" want "(\n|$)") {print}
    ' "$1"
}

# --- 9. The env-provisioner ClusterRole backs the ServiceAccount that the
#        deploy/stop/delete Jobs run the `erun` binary as -- a different
#        identity from api.envDeployer's own Role above, and one that this
#        chart test previously never asserted anything about. That gap is
#        exactly how one such gap shipped before: erun stop's `kubectl scale
#        deployment/... --replicas=0` (erun-common/stop.go) and erun deploy's
#        early-failure pod watch (erun-common/deploy_pod_watch.go) both shell
#        out to kubectl rather than calling client-go, so the require_role_
#        resource pattern above (grep a literal client-go call in
#        erun-backend-api's own Go source) does not apply here -- kubectl argv
#        text has no comparably stable literal to grep for without inventing a
#        brittle mapping. Pin the three grants directly instead. ---
rendered=$(render --set-string api.envDeployer.enabled=true)
provisioner_role="${work_root}/provisioner-role.yaml"
cluster_role "${rendered}" team-env-provisioner >"${provisioner_role}"
[ -s "${provisioner_role}" ] || fail "the env-provisioner ClusterRole must render when envDeployer is enabled"

grep -q 'resources: \["deployments/scale"\]' "${provisioner_role}" ||
    fail "the env-provisioner ClusterRole must grant deployments/scale, or erun stop's kubectl scale is forbidden in every provisioned namespace (#1080)"
grep -A1 'resources: \["deployments/scale"\]' "${provisioner_role}" | grep -q '"patch"' ||
    fail "the deployments/scale grant must include patch, the verb kubectl scale issues"

grep -q 'resources: \["pods"\]' "${provisioner_role}" ||
    fail "the env-provisioner ClusterRole must grant pods read, or erun deploy's early-failure pod watch and erun stop's desktop-session listing stay silently inert (#1080)"
grep -A1 'resources: \["pods"\]' "${provisioner_role}" | grep -q '"watch"' ||
    fail "the pods grant must include watch"

grep -q 'resources: \["events"\]' "${provisioner_role}" ||
    fail "the env-provisioner ClusterRole must grant events read, or a stuck pod's reason never reaches the recorded provision error (#1080)"

# --- 10. apps/replicasets read, separate from the deployments grant
#         above. Adding only pods/events/deployments-scale left
#         provisioning completely broken: helm's own readiness wait for a
#         Deployment walks Deployment -> its ReplicaSet -> that ReplicaSet's
#         ready count, so without list/watch on ReplicaSets `helm --wait`
#         never observes a healthy rollout finishing and rides out its full
#         timeout. This grant is necessary but -- like the grants before
#         it -- pinning it here only confirms a rule somebody already thought
#         of; it does not prove helm's wait actually succeeds against it. See
#         env_provisioner_rbac_e2e_test.go for a test that exercises the real
#         object graph against a live cluster's RBAC engine. ---
grep -q 'resources: \["replicasets"\]' "${provisioner_role}" ||
    fail "the env-provisioner ClusterRole must grant apps/replicasets read, or helm --wait can never observe a Deployment rollout finishing and every provision fails at the timeout (#1083)"
grep -A1 'resources: \["replicasets"\]' "${provisioner_role}" | grep -q '"list"' ||
    fail "the replicasets grant must include list, the verb helm's readiness wait issues"
grep -A1 'resources: \["replicasets"\]' "${provisioner_role}" | grep -q '"watch"' ||
    fail "the replicasets grant must include watch"

# --- 11. the deploy Job's post-deploy `erun expose` was refused twice
#         over by RBAC the provisioner role never granted -- creating the
#         Host-routing Ingress in the env namespace, and the `kubectl exec`
#         DNS write against the platform's PowerDNS singleton. The ingresses
#         grant belongs on this same ClusterRole (it is per-env, like
#         everything else here); the pods/exec grant does not (see the next
#         section) ---
grep -q 'resources: \["ingresses"\]' "${provisioner_role}" ||
    fail "the env-provisioner ClusterRole must grant networking.k8s.io/ingresses, or erun expose's Host-routing Ingress apply is forbidden in every provisioned env namespace (#1089)"
grep -A1 'resources: \["ingresses"\]' "${provisioner_role}" | grep -q '"create"' ||
    fail "the ingresses grant must include create"
grep -A1 'resources: \["ingresses"\]' "${provisioner_role}" | grep -q '"update"' ||
    fail "the ingresses grant must include update, or re-exposing an already-exposed env fails"
grep -A1 'resources: \["ingresses"\]' "${provisioner_role}" | grep -q '"patch"' ||
    fail "the ingresses grant must include patch, or re-exposing an already-exposed env fails"

# --- 11b. the deploy Job's post-deploy `erun expose` also provisions the
#          env's own namespaced cert-manager Issuer + Certificate through the
#          DNS-01 broker, so the Ingress's wildcard TLS secretName
#          actually gets populated ---
grep -q 'resources: \["issuers", "certificates"\]' "${provisioner_role}" ||
    fail "the env-provisioner ClusterRole must grant cert-manager.io/issuers+certificates, or the per-env TLS Issuer/Certificate apply is forbidden in every provisioned env namespace (#1093)"
grep -A1 'resources: \["issuers", "certificates"\]' "${provisioner_role}" | grep -q '"create"' ||
    fail "the issuers/certificates grant must include create"
grep -A1 'resources: \["issuers", "certificates"\]' "${provisioner_role}" | grep -q '"update"' ||
    fail "the issuers/certificates grant must include update, or re-exposing an already-exposed env fails"

# --- 12. pods/exec for the PowerDNS DNS write is scoped to a
#         namespaced Role bound in this chart's own release namespace (the
#         platform namespace), not the cluster-wide ClusterRole above -- a
#         cluster-wide grant would hand the deployer exec into every pod in
#         every provisioned tenant namespace ---
platform_role="${work_root}/platform-role.yaml"
role "${rendered}" team-env-provisioner-platform >"${platform_role}"
[ -s "${platform_role}" ] || fail "the env-provisioner-platform Role must render when envDeployer is enabled"
grep -q 'namespace: test' "${platform_role}" ||
    fail "the env-provisioner-platform Role must render in this chart's own release namespace, not a tenant env namespace"
grep -q 'resources: \["pods/exec"\]' "${platform_role}" ||
    fail "the env-provisioner-platform Role must grant pods/exec, or erun expose's PowerDNS DNS write is forbidden (#1089)"
grep -A1 'resources: \["pods/exec"\]' "${platform_role}" | grep -q '"create"' ||
    fail "the pods/exec grant must include create, the verb kubectl exec issues"

platform_binding="${work_root}/platform-binding.yaml"
awk -v want="  name: team-env-provisioner-platform" '
    BEGIN{RS="\n---\n"}
    $0 ~ /(^|\n)kind: RoleBinding(\n|$)/ && $0 ~ ("(^|\n)" want "(\n|$)") {print}
' "${rendered}" >"${platform_binding}"
[ -s "${platform_binding}" ] || fail "the env-provisioner-platform RoleBinding must render when envDeployer is enabled"
grep -q 'name: team-env-deployer' "${platform_binding}" ||
    fail "the env-provisioner-platform RoleBinding must bind the env-deployer ServiceAccount that the deploy Job -- and its chained erun expose -- runs as"

# --- 13. per-env TLS certificate provisioning rides the dns01 broker
#         values block: acmeEmail/acmeServer/webhookGroupName render as env
#         vars only when the broker itself is enabled, since the webhook shim
#         has nothing to call without it ---
rendered=$(render \
    --set-string api.dns01.enabled=true \
    --set-string api.dns01.servicesZone=services.example.com \
    --set-string api.dns01.acmeEmail=admin@example.com \
    --set-string api.dns01.acmeServer=https://acme-staging-v02.api.letsencrypt.org/directory \
    --set-string api.dns01.webhookGroupName=acme.example.io)
container "${rendered}" >"${core}"
grep -A1 'name: ERUN_ACME_EMAIL' "${core}" | grep -q 'value: "admin@example.com"' ||
    fail "api.dns01.acmeEmail must render as ERUN_ACME_EMAIL, or the deploy Job never mints a per-env TLS certificate (#1093)"
grep -A1 'name: ERUN_ACME_SERVER' "${core}" | grep -q 'value: "https://acme-staging-v02.api.letsencrypt.org/directory"' ||
    fail "api.dns01.acmeServer must render as ERUN_ACME_SERVER"
grep -A1 'name: ERUN_DNS01_WEBHOOK_GROUP_NAME' "${core}" | grep -q 'value: "acme.example.io"' ||
    fail "api.dns01.webhookGroupName must render as ERUN_DNS01_WEBHOOK_GROUP_NAME"

rendered=$(render --set-string api.dns01.acmeEmail=admin@example.com)
container "${rendered}" >"${core}"
grep -q 'ERUN_ACME_EMAIL' "${core}" &&
    fail "acmeEmail must not render when the dns01 broker itself is disabled -- there is no broker for the webhook shim to call"

# --- 14. The API's own public HTTPS edge (#1141). Every other externally
#         reachable component declares its own Ingress; the API could not, so
#         the endpoint every client actually holds a login record against was
#         the one thing nothing in the repo declared. ---

# The Ingress manifest, as its own block, so an assertion cannot pass by
# matching a line that belongs to the Service or the Deployment.
ingress() {
    awk '
        BEGIN{RS="\n---\n"}
        $0 ~ /(^|\n)kind: Ingress(\n|$)/ {print}
    ' "$1"
}

# Off by default: an existing deployment must not gain a public endpoint just by
# upgrading the chart.
rendered=$(render)
[ -z "$(ingress "${rendered}")" ] ||
    fail "the API Ingress must not render unless api.externalDomain is set -- an upgrade must never silently publish the API"

rendered=$(render --set-string api.externalDomain=api.example.com)
api_ingress="${work_root}/api-ingress.yaml"
ingress "${rendered}" >"${api_ingress}"
[ -s "${api_ingress}" ] || fail "api.externalDomain must render an Ingress for the API"
grep -q 'name: team-api' "${api_ingress}" ||
    fail "the Ingress must be named for the tenant's API, matching the Service it fronts"
grep -q 'host: api.example.com' "${api_ingress}" ||
    fail "api.externalDomain must become the Ingress host"
grep -q 'ingressClassName: traefik' "${api_ingress}" ||
    fail "the Ingress class must default to traefik, as erun-console's does"
grep -q 'secretName: team-api-tls' "${api_ingress}" ||
    fail "TLS must default to its own per-host secret, mirroring erun-console"
grep -q 'name: api' "${api_ingress}" ||
    fail "the Ingress backend must target the API Service's named port"
grep -q 'cert-manager.io/issuer' "${api_ingress}" &&
    fail "no issuer annotation must appear unless api.certManagerIssuer is set"

# An issuer annotation is how the certificate actually gets minted.
rendered=$(render \
    --set-string api.externalDomain=api.example.com \
    --set-string api.certManagerIssuer=frs-letsencrypt-http01)
ingress "${rendered}" >"${api_ingress}"
grep -q 'cert-manager.io/issuer: "frs-letsencrypt-http01"' "${api_ingress}" ||
    fail "api.certManagerIssuer must render the cert-manager issuer annotation"

# Reusing a wildcard the edge already issued, rather than minting a second
# certificate for a name the wildcard already covers.
rendered=$(render \
    --set-string api.externalDomain=api.team-prod.services.example.com \
    --set-string api.tlsSecretName=team-prod-wildcard-tls)
ingress "${rendered}" >"${api_ingress}"
grep -q 'secretName: team-prod-wildcard-tls' "${api_ingress}" ||
    fail "api.tlsSecretName must let an existing wildcard secret be reused"
grep -q 'cert-manager.io/issuer' "${api_ingress}" &&
    fail "reusing a wildcard must not also request issuance"

# An operator-supplied annotation (middleware, rate limits, auth) must pass
# through, like the console's.
rendered=$(render \
    --set-string api.externalDomain=api.example.com \
    --set-string 'api.ingressAnnotations.traefik\.ingress\.kubernetes\.io/router\.entrypoints=websecure')
ingress "${rendered}" >"${api_ingress}"
grep -q 'traefik.ingress.kubernetes.io/router.entrypoints: "websecure"' "${api_ingress}" ||
    fail "api.ingressAnnotations must pass through to the Ingress"

# Explicitly empty TLS secret: plain HTTP, for an edge terminating TLS ahead of
# the ingress. The Ingress must still render, without a tls block.
rendered=$(render \
    --set-string api.externalDomain=api.example.com \
    --set-string api.tlsSecretName="")
ingress "${rendered}" >"${api_ingress}"
[ -s "${api_ingress}" ] || fail "an empty tlsSecretName must still render the Ingress"
grep -q 'tls:' "${api_ingress}" &&
    fail "an empty tlsSecretName must render no tls block rather than a dangling secret reference"

# --- 15. The delete Job's pre-teardown challenge retraction needs read access
#         to cert-manager's ACME chain and delete on certificates (#1183). It
#         shipped without them, so its own reads were Forbidden, that was read
#         as "no challenges here", and the retraction did nothing at all while
#         reporting success. ---
rendered=$(render --set-string api.envDeployer.enabled=true)
provisioner="${work_root}/env-provisioner.yaml"
awk -v want="  name: team-env-provisioner" '
    BEGIN{RS="\n---\n"}
    $0 ~ /(^|\n)kind: ClusterRole(\n|$)/ && $0 ~ ("(^|\n)" want "(\n|$)") {print}
' "${rendered}" >"${provisioner}"
[ -s "${provisioner}" ] || fail "the env-provisioner ClusterRole must render when envDeployer is enabled"

grep -q 'apiGroups: \["acme.cert-manager.io"\]' "${provisioner}" ||
    fail "the env-provisioner must be able to read acme.cert-manager.io, or the delete Job cannot see a challenge holding the namespace and the retraction is silently inert (#1183)"
grep -A2 'apiGroups: \["acme.cert-manager.io"\]' "${provisioner}" | grep -q 'challenges' ||
    fail "the acme.cert-manager.io grant must cover challenges -- the Challenge finalizer is what blocks the namespace"
grep -A2 'apiGroups: \["cert-manager.io"\]' "${provisioner}" | grep -q '"delete"' ||
    fail "the env-provisioner must be able to delete certificates, or the retraction cannot cascade to the Challenge that holds the namespace"
grep -A2 'apiGroups: \["cert-manager.io"\]' "${provisioner}" | grep -q '"list"' ||
    fail "the env-provisioner must be able to list certificates, since the retraction deletes them with --all"

# --- 16. Identity administration (issue #1209): with no platform.authHost,
#         the pod is byte-for-byte unchanged -- no zitadel-management-pat
#         volume, no ERUN_ZITADEL_* env vars. With it set, the PAT Secret
#         mounts optional (an env with no zitadel component, or one still
#         bootstrapping, must still start), and the external domain used for
#         the outgoing Host header is the same platform.authHost the OIDC
#         issuer above already uses. ---
rendered=$(render)
container "${rendered}" >"${core}"
grep -q 'ERUN_ZITADEL_MANAGEMENT_API_URL' "${core}" &&
    fail "ERUN_ZITADEL_MANAGEMENT_API_URL must not render without platform.authHost -- there is no external domain to target"
grep -q 'zitadel-management-pat' "${rendered}" &&
    fail "the zitadel-management-pat volume must not render without platform.authHost"

rendered=$(render --set-string platform.authHost=auth.example.com)
container "${rendered}" >"${core}"
grep -A1 'name: ERUN_ZITADEL_MANAGEMENT_API_URL' "${core}" | grep -q 'value: "http://team-zitadel:8080"' ||
    fail "ERUN_ZITADEL_MANAGEMENT_API_URL must default to the tenant's own zitadel Service address"
grep -A1 'name: ERUN_ZITADEL_EXTERNAL_DOMAIN' "${core}" | grep -q 'value: "auth.example.com"' ||
    fail "ERUN_ZITADEL_EXTERNAL_DOMAIN must default to platform.authHost, the same host the OIDC issuer uses"
grep -A1 'name: ERUN_ZITADEL_MANAGEMENT_PAT_PATH' "${core}" | grep -q 'value: "/etc/erun/zitadel-management/admin-sa.pat"' ||
    fail "ERUN_ZITADEL_MANAGEMENT_PAT_PATH must point at the mounted admin-sa.pat key"
grep -A2 'name: zitadel-management-pat' "${rendered}" | grep -q 'secretName: team-zitadel-pats' ||
    fail "the zitadel-management-pat volume must default to <tenant>-zitadel-pats, the erun-zitadel chart's own Secret"
grep -A3 'name: zitadel-management-pat' "${rendered}" | grep -q 'optional: true' ||
    fail "the zitadel-management-pat volume must be optional -- an env with no zitadel component must still start"

# --- 17. The merge queue needs DBOSContext too (server.go's newMergeQueue),
#         and its RBAC/env wiring mirrors the release queue's -- create/get/
#         list/watch/delete on Jobs plus pods/pods-log read, and the workspace
#         claim + repo path are required (unlike the release queue's, which are
#         optional): the merge Job fetches, commits, and pushes, so it needs a
#         real writable checkout, not whatever happens to be baked into the
#         image. ---
rendered=$(render \
    --set-string api.mergeQueue.enabled=true \
    --set-string api.mergeQueue.namespace=team-ux \
    --set-string api.mergeQueue.workspaceClaim=team-devops-worktree \
    --set-string api.mergeQueue.repoPath=/home/erun/git/erun)
container "${rendered}" >"${core}"
grep -q 'name: DBOS_SYSTEM_DATABASE_URL' "${core}" ||
    fail "api.mergeQueue.enabled must also render DBOS_SYSTEM_DATABASE_URL, or the merge queue never dispatches"
grep -q 'name: ERUN_MERGE_NAMESPACE' "${core}" ||
    fail "api.mergeQueue.enabled must render ERUN_MERGE_NAMESPACE"
grep -A1 'name: ERUN_MERGE_NAMESPACE' "${core}" | grep -q 'value: "team-ux"' ||
    fail "api.mergeQueue.namespace must render as ERUN_MERGE_NAMESPACE"
grep -A1 'name: ERUN_MERGE_SERVICE_ACCOUNT' "${core}" | grep -q 'value: "team-devops"' ||
    fail "the merge queue's service account must default to <tenant>-devops, the environment's own runtime SA"
grep -A1 'name: ERUN_MERGE_WORKSPACE_CLAIM' "${core}" | grep -q 'value: "team-devops-worktree"' ||
    fail "api.mergeQueue.workspaceClaim must render as ERUN_MERGE_WORKSPACE_CLAIM"
grep -A1 'name: ERUN_MERGE_REPO_PATH' "${core}" | grep -q 'value: "/home/erun/git/erun"' ||
    fail "api.mergeQueue.repoPath must render as ERUN_MERGE_REPO_PATH"

merge_role="${work_root}/merge-role.yaml"
role "${rendered}" team-api-merge-queue >"${merge_role}"
[ -s "${merge_role}" ] || fail "the merge-queue Role must render when the merge queue is enabled"
require_role_resource '.BatchV1().Jobs(' jobs "${merge_role}"
require_role_resource '.CoreV1().Pods(' pods "${merge_role}"
require_role_resource '.GetLogs(' 'pods/log' "${merge_role}"

merge_binding="${work_root}/merge-binding.yaml"
awk -v want="  name: team-api-merge-queue" '
    BEGIN{RS="\n---\n"}
    $0 ~ /(^|\n)kind: RoleBinding(\n|$)/ && $0 ~ ("(^|\n)" want "(\n|$)") {print}
' "${rendered}" >"${merge_binding}"
[ -s "${merge_binding}" ] || fail "the merge-queue RoleBinding must render when the merge queue is enabled"
grep -q 'namespace: team-ux' "${merge_binding}" ||
    fail "the merge-queue RoleBinding must bind in the agent environment's own namespace, not the platform namespace"

# Neither the namespace, the workspace claim, nor the repo path may be left
# unset -- provision.MergeConfig.Configured() requires all of them, and a
# review promoted to MERGE with a half-wired queue is worse than one that was
# never wired at all: it looks configured but never gates anything. Called
# directly (not via render()) so a helm failure here is the assertion, not a
# script-terminating error.
helm template test "${chart_dir}" --namespace test --set tenant=team --set environment=prod \
    --set-string api.mergeQueue.enabled=true >/dev/null 2>&1 &&
    fail "api.mergeQueue.enabled without a namespace must fail to render"
helm template test "${chart_dir}" --namespace test --set tenant=team --set environment=prod \
    --set-string api.mergeQueue.enabled=true --set-string api.mergeQueue.namespace=team-ux >/dev/null 2>&1 &&
    fail "api.mergeQueue.enabled without a workspaceClaim must fail to render"
helm template test "${chart_dir}" --namespace test --set tenant=team --set environment=prod \
    --set-string api.mergeQueue.enabled=true \
    --set-string api.mergeQueue.namespace=team-ux \
    --set-string api.mergeQueue.workspaceClaim=team-devops-worktree >/dev/null 2>&1 &&
    fail "api.mergeQueue.enabled without a repoPath must fail to render"

# Off by default: byte-for-byte unchanged, same as the release queue.
rendered=$(render)
container "${rendered}" | grep -q 'ERUN_MERGE_' &&
    fail "no ERUN_MERGE_* env var should render unless the merge queue is enabled"

# --- 18. The white-label trio GET /v1/platform serves a pre-sign-in client:
#         its own docs link, tagline, and logo. They travel the same
#         platform.* path consoleUrl and brand already do, and each renders as
#         an empty string when unset so a client keeps falling back to its
#         bundled default rather than showing a blank hero or a broken image. ---
rendered=$(render)
container "${rendered}" >"${core}"
for var in ERUN_PLATFORM_DOCS_URL ERUN_PLATFORM_TAGLINE ERUN_PLATFORM_LOGO_URL; do
    grep -q "name: ${var}" "${core}" ||
        fail "${var} must always render, so an unset value reaches the client as \"\" rather than a missing key"
    grep -A1 "name: ${var}" "${core}" | grep -q 'value: ""' ||
        fail "${var} must render as an empty string when the platform config sets none"
done

rendered=$(render \
    --set-string platform.docsUrl=https://docs.example.com \
    --set-string platform.tagline='Example ships faster.' \
    --set-string platform.logoUrl=https://cdn.example.com/logo.svg)
container "${rendered}" >"${core}"
grep -A1 'name: ERUN_PLATFORM_DOCS_URL' "${core}" | grep -q 'value: "https://docs.example.com"' ||
    fail "platform.docsUrl must render as ERUN_PLATFORM_DOCS_URL"
grep -A1 'name: ERUN_PLATFORM_TAGLINE' "${core}" | grep -q 'value: "Example ships faster."' ||
    fail "platform.tagline must render as ERUN_PLATFORM_TAGLINE"
grep -A1 'name: ERUN_PLATFORM_LOGO_URL' "${core}" | grep -q 'value: "https://cdn.example.com/logo.svg"' ||
    fail "platform.logoUrl must render as ERUN_PLATFORM_LOGO_URL"

echo "PASS: erun-backend-api DBOS wiring + public API edge + retraction RBAC + identity admin wiring + merge queue wiring + platform white-label discovery"
