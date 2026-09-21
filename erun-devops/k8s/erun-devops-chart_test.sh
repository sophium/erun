#!/bin/sh

# Tests for the erun-devops runtime chart's pod shape: the runtime container is
# the environment's only long-lived application container and serves the MCP
# edge itself, the MCP auth env and key mount land on it, the runtime image
# override still applies, a disabled edge renders no port and no fronting
# Service (an enabled one gets both), and the volumes the runtime user writes
# are handed to it without a pod-wide fsGroup.
#
# Lives beside the chart rather than inside it: helm renders every file under
# templates/, and the chart's own contents feed the runtime image fingerprint.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-devops"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the runtime chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-devops-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

render() {
    out="${work_root}/render.yaml"
    helm template test "${chart_dir}" \
        --set tenant=team \
        --set environment=dev \
        --set worktreeStorage=pvc \
        --set worktreeRepoName=petios \
        "$@" >"${out}" || fail "helm template failed"
    printf '%s\n' "${out}"
}

# The pod's containers: section, with initContainers and volumes excluded so the
# binfmt installer never counts as an application container.
containers_section() {
    awk '/^      containers:/{inside=1;next} /^      volumes:/{inside=0} inside' "$1"
}

init_containers_section() {
    awk '/^      initContainers:/{inside=1;next} /^      containers:/{inside=0} inside' "$1"
}

init_container_names() {
    init_containers_section "$1" | sed -n 's/^          name: \(.*\)$/\1/p'
}

# The init container entry named $2, as its own block. Comment lines are
# dropped: they lead the entry they describe, so they would otherwise be read
# as part of the preceding one.
init_container() {
    init_containers_section "$1" | awk -v want="          name: $2" '
        /^        #/{next}
        /^        - image: /{n++; buf[n]=""}
        n{buf[n]=buf[n] $0 "\n"; if ($0==want) hit=n}
        END{if (hit) printf "%s", buf[hit]}
    '
}

container_names() {
    containers_section "$1" | sed -n 's/^          name: \(.*\)$/\1/p'
}

# The first container entry, i.e. the runtime container.
runtime_container() {
    containers_section "$1" | awk '/^        - image: /{c++} c==1'
}

# The second container entry, i.e. the docker daemon sidecar.
dind_container() {
    containers_section "$1" | awk '/^        - image: /{c++} c==2'
}

# --- 1. One application container plus the docker daemon sidecar ---
rendered=$(render)
names=$(container_names "${rendered}")
[ "${names}" = "erun-devops
erun-dind" ] || fail "expected erun-devops + erun-dind containers, got: ${names}"

# --- 2. The MCP port and the enable gate land on the runtime container ---
grep -q '^            - name: ERUN_MCP_ENABLED$' "${rendered}" ||
    fail "ERUN_MCP_ENABLED should be wired on the runtime container"
grep -q '^              name: mcp$' "${rendered}" ||
    fail "the mcp containerPort should be declared"
[ "$(grep -c '^              name: mcp$' "${rendered}")" = "1" ] ||
    fail "the mcp containerPort should be declared exactly once"

# --- 3. MCP auth env and the desktop key mount land on the runtime container ---
rendered=$(render \
    --set mcpAuth.enabled=true \
    --set mcpAuth.secretName=erun-mcp-auth \
    --set-string mcpAuth.issuer=file:///home/erun/desktopid.pub \
    --set-string mcpAuth.audience=erun-mcp:team/dev)
runtime_block="${work_root}/runtime.yaml"
runtime_container "${rendered}" >"${runtime_block}"
grep -q 'ERUN_MCP_TRUSTED_ISSUER' "${runtime_block}" ||
    fail "the trusted issuer belongs on the runtime container"
grep -q 'ERUN_MCP_AUDIENCE' "${runtime_block}" ||
    fail "the audience belongs on the runtime container"
grep -q '^            - name: mcp-auth$' "${runtime_block}" ||
    fail "the mcp-auth key mount belongs on the runtime container"

# --- 4. MCP auth disabled renders no auth env and no key mount ---
rendered=$(render)
grep -q 'ERUN_MCP_TRUSTED_ISSUER' "${rendered}" &&
    fail "no trusted issuer should render when mcp auth is disabled"
grep -q 'ERUN_MCP_AUDIENCE' "${rendered}" &&
    fail "no audience should render when mcp auth is disabled"
grep -q 'name: mcp-auth' "${rendered}" &&
    fail "no mcp-auth volume or mount should render without a secret name"

# --- 5. The runtime image override still selects the tenant's own image ---
rendered=$(render --set-string imageOverrides.erun-devops=reg.example/petios-devops:1.2.3)
grep -q '^        - image: reg.example/petios-devops:1.2.3$' "${rendered}" ||
    fail "imageOverrides.erun-devops should select the runtime image"

# --- 6. A disabled edge advertises no MCP port ---
rendered=$(render --set mcpEnabled=false)
grep -A1 '^            - name: ERUN_MCP_ENABLED$' "${rendered}" | grep -q '"false"' ||
    fail "mcpEnabled=false should reach the container env"
grep -q '^              name: mcp$' "${rendered}" &&
    fail "a disabled edge should advertise no mcp containerPort"
grep -q '^kind: Service$' "${rendered}" &&
    fail "a disabled edge should render no fronting Service"

# --- 6b. An enabled edge is fronted by a Service `erun expose` can route to:
# named team-mcp (TenantResourcePrefix(tenant)+"-mcp", what expose.go derives
# from tenant + service name alone), selecting this release's pods, port 80
# (the public hostname's default, no --port needed) mapped onto the named mcp
# containerPort. ---
rendered=$(render)
grep -q '^kind: Service$' "${rendered}" ||
    fail "an enabled edge should render a fronting Service"
service_block="${work_root}/service.yaml"
awk '/^kind: Service$/{f=1} f{print} f && /^---$/{exit}' "${rendered}" >"${service_block}"
grep -q '^  name: team-mcp$' "${service_block}" ||
    fail "the fronting Service should be named team-mcp"
grep -q '^    app: test$' "${service_block}" ||
    fail "the fronting Service should select this release's pods"
grep -q '^      port: 80$' "${service_block}" ||
    fail "the fronting Service should listen on port 80"
grep -q '^      targetPort: mcp$' "${service_block}" ||
    fail "the fronting Service should target the named mcp containerPort"

# --- 7. A stopped environment renders replicas: 0, a running one replicas: 1 ---
# This is what makes a stop durable: without the chart value the scale patch is
# drift the next helm upgrade silently reverts, restarting a pod the operator
# deliberately scaled away to give its capacity back to the node.
rendered=$(render --set stopped=true)
grep -q '^  replicas: 0$' "${rendered}" ||
    fail "stopped=true should render replicas: 0"

rendered=$(render)
grep -q '^  replicas: 1$' "${rendered}" ||
    fail "an environment with no stop recorded should render replicas: 1"

# --- 8. An enabled metrics listener gets its containerPort, its env vars, and
# a NetworkPolicy that makes the pod's ingress default-deny and then re-permits
# exactly ssh, mcp, and the metrics port by number -- ssh/mcp from any source,
# the metrics port only from same-namespace traffic plus a namespace labelled
# as a scraper. The policy is what isolates the pod: a port it does not name is
# unreachable from other pods, so the permitted set below is a security
# boundary and is enumerated as one. ---
rendered=$(render)
grep -q '^            - name: ERUN_METRICS_ENABLED$' "${rendered}" ||
    fail "ERUN_METRICS_ENABLED should be wired on the runtime container"
grep -A1 '^            - name: ERUN_METRICS_ENABLED$' "${rendered}" | grep -q '"true"' ||
    fail "ERUN_METRICS_ENABLED should default to true"
grep -q '^              name: metrics$' "${rendered}" ||
    fail "the metrics containerPort should be declared"
[ "$(grep -c '^              name: metrics$' "${rendered}")" = "1" ] ||
    fail "the metrics containerPort should be declared exactly once"

grep -q '^kind: NetworkPolicy$' "${rendered}" ||
    fail "an enabled metrics listener should render a NetworkPolicy"
netpol_block="${work_root}/netpol.yaml"
awk '/^kind: NetworkPolicy$/{f=1} f{print} f && /^---$/{exit}' "${rendered}" >"${netpol_block}"
grep -q '^  name: test-metrics$' "${netpol_block}" ||
    fail "the NetworkPolicy should be named after the release"
grep -q '^      app: test$' "${netpol_block}" ||
    fail "the NetworkPolicy should select this release's pods"
# ssh (17022) and mcp (17000) each get their own ports-only, from-less rule:
# the pod is default-deny for every port the policy does not name, so those two
# rules are what keeps ssh/mcp reachable from anywhere, and they stay reachable
# only because they are named.
grep -q '^        - port: 17022$' "${netpol_block}" ||
    fail "the NetworkPolicy should keep the ssh port open"
grep -q '^        - port: 17000$' "${netpol_block}" ||
    fail "the NetworkPolicy should keep the mcp port open"
grep -q '^        - port: 9100$' "${netpol_block}" ||
    fail "the NetworkPolicy should scope the metrics port"
grep -q 'network-policy/erun-metrics-scraper: "true"$' "${netpol_block}" ||
    fail "the NetworkPolicy should admit a namespace labelled as a metrics scraper"

# The permitted set is enumerable, so enumerate it. A rule that omitted its
# ports: key would admit every port on the pod, and a port the chart stops
# rendering but the policy still names would leave that port open on a pod that
# no longer serves it.
ingress_block="${work_root}/netpol-ingress.yaml"
awk '/^  ingress:/{inside=1;next} inside' "${netpol_block}" >"${ingress_block}"
[ "$(grep -c '^    - ' "${ingress_block}")" = "3" ] ||
    fail "the NetworkPolicy should render exactly three ingress rules"
[ "$(grep -c '^    - ports:$' "${ingress_block}")" = "3" ] ||
    fail "every ingress rule must name its ports -- a rule without ports: admits every port"
[ "$(sed -n 's/^        - port: //p' "${ingress_block}" | sort -n | tr '\n' ' ')" = "9100 17000 17022 " ] ||
    fail "the permitted set should be exactly the metrics, mcp, and ssh ports"
[ "$(awk '/^    - ports:$/{n++} /^      from:$/{print n}' "${ingress_block}")" = "3" ] ||
    fail "only the third (metrics) rule should carry a from: restriction"

# The permitted set follows the resolved ports rather than being fixed: with
# the MCP edge off, 17000 must leave the policy too, or the pod would keep
# re-permitting a port it no longer serves.
rendered=$(render --set mcpEnabled=false)
netpol_block="${work_root}/netpol-mcp-disabled.yaml"
awk '/^kind: NetworkPolicy$/{f=1} f{print} f && /^---$/{exit}' "${rendered}" >"${netpol_block}"
grep -q '^        - port: 17000$' "${netpol_block}" &&
    fail "a disabled MCP edge should not stay in the NetworkPolicy"
grep -q '^        - port: 17022$' "${netpol_block}" ||
    fail "the ssh port should stay permitted without the MCP edge"
grep -q '^        - port: 9100$' "${netpol_block}" ||
    fail "the metrics port should stay permitted without the MCP edge"

# --- 9. A disabled metrics listener renders no containerPort and no
# NetworkPolicy at all, so a deploy that turns metrics off is byte-for-byte
# the pre-metrics pod shape rather than an empty/broken policy ---
rendered=$(render --set metricsEnabled=false)
grep -A1 '^            - name: ERUN_METRICS_ENABLED$' "${rendered}" | grep -q '"false"' ||
    fail "metricsEnabled=false should reach the container env"
grep -q '^              name: metrics$' "${rendered}" &&
    fail "a disabled metrics listener should advertise no containerPort"
grep -q '^kind: NetworkPolicy$' "${rendered}" &&
    fail "a disabled metrics listener should render no NetworkPolicy"

# --- 8. An AWS env with a region exports it ---
rendered=$(render --set-string cloudContext.provider=aws --set-string cloudContext.region=eu-west-2)
grep -A1 '^            - name: AWS_REGION$' "${rendered}" | grep -q '"eu-west-2"' ||
    fail "a resolved region should reach AWS_REGION"
grep -A1 '^            - name: ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION$' "${rendered}" | grep -q '"eu-west-2"' ||
    fail "the small/fast model region should default to the env's region"

# --- 9. An AWS env with no resolved region exports no region at all ---
# An empty AWS_REGION overrides the pod profile's own region instead of falling
# back to it, so the variable has to be absent rather than empty.
rendered=$(render --set-string cloudContext.provider=aws)
grep -q '^            - name: AWS_REGION$' "${rendered}" &&
    fail "no AWS_REGION should render when no region resolved"
grep -q '^            - name: ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION$' "${rendered}" &&
    fail "no small/fast model region should render when no region resolved"
grep -q '^            - name: ERUN_CLOUD_REGION$' "${rendered}" ||
    fail "erun's own ERUN_CLOUD_REGION stays wired so the in-pod config sync still projects it"

# --- 10. Host credentials select the profile erun writes into the pod ---
rendered=$(render --set-string cloudContext.provider=aws --set cloudContext.useHostCredentials=true)
grep -A1 '^            - name: AWS_PROFILE$' "${rendered}" | grep -q '"erun-host"' ||
    fail "useHostCredentials should select the erun-host profile"

# --- 11. A pvc worktree adopts an existing tree instead of shadowing it ---
# The claim is mounted at the worktree path, so a tree that predates the claim is
# only reachable from a container where the claim does not shadow the home
# volume. Both volumes therefore have to be staged elsewhere, at distinct paths.
rendered=$(render)
init_block="${work_root}/init.yaml"
init_containers_section "${rendered}" >"${init_block}"
grep -q '^          name: adopt-worktree$' "${init_block}" ||
    fail "a pvc worktree should render the adoption init container"
grep -q 'erun-adopt-worktree "/mnt/erun-home/git/petios" "/mnt/erun-worktree"' "${init_block}" ||
    fail "the adoption init container should stage the legacy tree and the claim as separate paths"

home_stage=$(awk '/^            - name: erun-home$/{getline; print}' "${init_block}" | sed -n 's/^              mountPath: "\(.*\)"$/\1/p')
claim_stage=$(awk '/^            - name: repo-worktree$/{getline; print}' "${init_block}" | sed -n 's/^              mountPath: "\(.*\)"$/\1/p')
[ -n "${home_stage}" ] || fail "the adoption init container must mount the home volume"
[ -n "${claim_stage}" ] || fail "the adoption init container must mount the worktree claim"
[ "${home_stage}" != "${claim_stage}" ] || fail "the two volumes must stage at distinct paths"
for staged in "${home_stage}" "${claim_stage}"; do
    [ "${staged}" = "/home/erun/git/petios" ] &&
        fail "staging at the live worktree path would reproduce the shadowing this prevents"
    case "${staged}" in
        /home/erun | /home/erun/*)
            fail "staging under /home/erun lets the worktree claim shadow the tree being adopted"
            ;;
    esac
done

# --- 12. A host worktree renders no adoption container ---
# The tree lives on the node, not on either volume; there is nothing to adopt.
rendered=$(render --set worktreeStorage=host --set-string worktreeHostPath=/host/git/petios)
init_containers_section "${rendered}" >"${init_block}"
grep -q 'adopt-worktree' "${init_block}" &&
    fail "a host worktree should render no adoption init container"

# --- 13. A sourceless runtime env renders no adoption container ---
rendered=$(render --set worktreeStorage=none)
init_containers_section "${rendered}" >"${init_block}"
grep -q 'adopt-worktree' "${init_block}" &&
    fail "an env with no worktree volume should render no adoption init container"

# --- 14. The pod joins the dind image's docker group ---
# Group membership is what survives a daemon restart: dockerd recreates the
# socket 0660 root:docker, so a runtime container that is not in that group
# depends on something widening each new socket before it is used.
rendered=$(render)
pod_security_context() {
    awk '/^      securityContext:/{inside=1;next} /^      [a-zA-Z]/{inside=0} inside' "$1"
}
pod_security_context "${rendered}" | grep -q '^        supplementalGroups:$' ||
    fail "the pod securityContext should declare supplementalGroups"
pod_security_context "${rendered}" | grep -q '^          - 2375$' ||
    fail "the pod should join the dind image's docker gid 2375"

# --- 15. The docker gid follows the dind base when it is overridden ---
rendered=$(render --set dindDockerGid=4242)
pod_security_context "${rendered}" | grep -q '^          - 4242$' ||
    fail "dindDockerGid should select the pod's supplemental group"
pod_security_context "${rendered}" | grep -q '^          - 2375$' &&
    fail "an overridden gid should replace the default, not accompany it"

# --- 16. The socket hook waits for a live daemon, not for a socket file ---
# A SIGKILLed dockerd cannot unlink its socket, so the file outlives it on the
# shared emptyDir: a file-existence wait returns at once and acts on the inode
# the restarting daemon is about to replace.
rendered=$(render)
dind_block="${work_root}/dind.yaml"
dind_container "${rendered}" >"${dind_block}"
hook_line=$(awk '/^            postStart:$/{inside=1;next} inside && /^                  - /{line=$0} END{print line}' "${dind_block}")
hook=${hook_line#*- }
hook=${hook#\'}
hook=${hook%\'}
[ -n "${hook}" ] || fail "the dind container should carry a postStart socket hook"
case "${hook}" in
    *'! -S /var/run/docker.sock'*)
        fail "waiting on mere socket existence is the race this replaced"
        ;;
esac
case "${hook}" in
    *"docker -H unix:///var/run/docker.sock info"*) ;;
    *) fail "the hook should probe the daemon rather than the socket file" ;;
esac
case "${hook}" in
    *'[ "$i" -ge '*) ;;
    *) fail "the hook's wait must be bounded; kubernetes never times a postStart hook out" ;;
esac
case "${hook}" in
    *"chmod 0666 /var/run/docker.sock 2>/dev/null || true"*) ;;
    *) fail "the chmod is a fallback and must not fail the hook" ;;
esac

# --- 17. The pod declares no fsGroup, and still joins the docker group ---
# fsGroup cannot be scoped to a subset of a pod's volumes, so it also reached
# the docker state claim and had the kubelet rewrite every image layer under it
# group-writable on each start, breaking sshd, postgres and any baked 0600
# secret inside those images. Group membership must survive its removal.
rendered=$(render)
grep -q 'fsGroup' "${rendered}" &&
    fail "no fsGroup may render: it relabels the docker state claim along with everything else"
pod_security_context "${rendered}" | grep -q '^        supplementalGroups:$' ||
    fail "dropping fsGroup must not drop the pod's docker group membership"

# --- 18. An init container hands the runtime user the claims it writes ---
# What fsGroup was actually buying: a freshly provisioned claim mounts empty and
# root-owned, so uid 1000 has nowhere to write. Ownership of the mount points is
# established explicitly instead, and only for the volumes the runtime user
# writes — never the docker state claim this replaced fsGroup to protect.
prepare_block="${work_root}/prepare.yaml"
init_container "${rendered}" prepare-volumes >"${prepare_block}"
[ -s "${prepare_block}" ] || fail "the runtime pod should render the volume-preparation init container"
grep -q '^            runAsUser: 0$' "${prepare_block}" ||
    fail "only root can chown a freshly provisioned claim"
grep -q 'chown 1000:1000 "/mnt/erun-home"' "${prepare_block}" ||
    fail "the home claim's mount point should be handed to the runtime user"
grep -q 'chown 1000:1000 "/mnt/erun-worktree"' "${prepare_block}" ||
    fail "a pvc worktree claim's mount point should be handed to the runtime user"
grep -q '^            - name: erun-home$' "${prepare_block}" ||
    fail "the preparation init container must mount the home volume"
grep -q '^            - name: repo-worktree$' "${prepare_block}" ||
    fail "the preparation init container must mount the pvc worktree claim"
grep -q 'docker-state' "${prepare_block}" &&
    fail "the docker state claim must stay out of the preparation container; its layer permissions are dockerd's"
grep -q 'docker-socket' "${prepare_block}" &&
    fail "the docker socket emptyDir is the daemon's and must stay out of the preparation container"

# --- 19. Preparation runs before adoption ---
# The adoption copies the legacy tree into the claim as uid 1000, so it has to
# see a claim that has already been handed over.
order="${work_root}/init-order.txt"
init_container_names "${rendered}" >"${order}"
prepare_index=$(grep -n '^prepare-volumes$' "${order}" | cut -d: -f1)
adopt_index=$(grep -n '^adopt-worktree$' "${order}" | cut -d: -f1)
[ -n "${prepare_index}" ] || fail "the preparation init container should be named in the init order"
[ -n "${adopt_index}" ] || fail "a pvc worktree should still render the adoption init container"
[ "${prepare_index}" -lt "${adopt_index}" ] ||
    fail "preparation must precede adoption, which writes the claim as uid 1000"

# --- 20. A host worktree is the operator's node directory and is never chowned ---
rendered=$(render --set worktreeStorage=host --set-string worktreeHostPath=/host/git/petios)
init_container "${rendered}" prepare-volumes >"${prepare_block}"
grep -q 'chown 1000:1000 "/mnt/erun-home"' "${prepare_block}" ||
    fail "the home claim needs preparing whatever the worktree storage is"
grep -q 'repo-worktree' "${prepare_block}" &&
    fail "a host worktree lives on the node; the chart must not take ownership of it"

# --- 21. A sourceless runtime env still prepares its home claim ---
rendered=$(render --set worktreeStorage=none)
init_container "${rendered}" prepare-volumes >"${prepare_block}"
grep -q 'chown 1000:1000 "/mnt/erun-home"' "${prepare_block}" ||
    fail "an env with no worktree volume still needs its home claim prepared"
grep -q 'repo-worktree' "${prepare_block}" &&
    fail "an env with no worktree volume has no claim to prepare"

# --- 22. The rendered ServiceAccount carries the env's image-pull credentials ---
# A pod that gets its own ServiceAccount stops inheriting the namespace's
# `default` SA and whatever registry secret it holds — the runtime SA has to
# carry the same credential explicitly, or a private runtime image never pulls.
service_account_block() {
    awk '/^kind: ServiceAccount$/{f=1} f{print} f && /^---$/{exit}' "$1"
}

rendered=$(render)
service_account_block "${rendered}" | grep -q 'imagePullSecrets' &&
    fail "no imagePullSecrets should render on the ServiceAccount when none are configured"

rendered=$(render --set-string 'imagePullSecrets[0].name=ghcr-pull')
sa_block="${work_root}/sa.yaml"
service_account_block "${rendered}" >"${sa_block}"
grep -q '^imagePullSecrets:$' "${sa_block}" ||
    fail "a configured image pull secret should render on the runtime ServiceAccount"
grep -q '^  - name: ghcr-pull$' "${sa_block}" ||
    fail "the runtime ServiceAccount's imagePullSecrets should name the configured secret"

# --- 23. The pod's total resource ceiling matches the namespace quota floor
# both erun-common (MinimumRuntimeNamespaceQuota) and erun-backend-api
# (repository.DefaultMax*) derive from it. A ResourceQuota sums every
# container in the pod, so this locks in that the runtime container's limits,
# the dind sidecar's limits, and all three PVCs add up to exactly what those
# Go sources expect to admit (#1061: they used to assume one container and one
# quota-width default, and silently drifted apart from the pod's real shape). --
container_limit_cpu() {
    awk '/^          resources:/{f=1} f && /cpu:/{print; exit}' "$1" | sed -n 's/.*cpu: "\(.*\)"/\1/p'
}
container_limit_memory() {
    awk '/^          resources:/{f=1} f && /memory:/{print; exit}' "$1" | sed -n 's/.*memory: "\(.*\)"/\1/p'
}
pvc_storage_requests() {
    awk '/^kind: PersistentVolumeClaim$/{f=1} f && /storage:/{print; f=0}' "$1" |
        sed -n 's/.*storage: "\{0,1\}\([0-9]*\)Gi"\{0,1\}/\1/p'
}

rendered=$(render --set worktreeStorage=pvc)
runtime_block="${work_root}/runtime-resources.yaml"
runtime_container "${rendered}" >"${runtime_block}"
dind_block="${work_root}/dind-resources.yaml"
dind_container "${rendered}" >"${dind_block}"

runtime_cpu=$(container_limit_cpu "${runtime_block}")
dind_cpu=$(container_limit_cpu "${dind_block}")
runtime_memory=$(container_limit_memory "${runtime_block}" | sed 's/Mi$//')
dind_memory=$(container_limit_memory "${dind_block}" | sed 's/Mi$//')
total_cpu_millicores=$(awk -v a="${runtime_cpu}" -v b="${dind_cpu}" 'BEGIN{printf "%d", (a+b)*1000}')
total_memory_mb=$((runtime_memory + dind_memory))
total_storage_gb=0
for gb in $(pvc_storage_requests "${rendered}"); do
    total_storage_gb=$((total_storage_gb + gb))
done

[ "${total_cpu_millicores}" = "8000" ] ||
    fail "pod cpu limits should sum to 8000m (erun-devops ${runtime_cpu} + erun-dind ${dind_cpu}), got ${total_cpu_millicores}m"
[ "${total_memory_mb}" = "36864" ] ||
    fail "pod memory limits should sum to 36864Mi (erun-devops ${runtime_memory}Mi + erun-dind ${dind_memory}Mi), got ${total_memory_mb}Mi"
[ "${total_storage_gb}" = "72" ] ||
    fail "the pod's PVCs should sum to 72Gi (home 2Gi + docker 50Gi + worktree 20Gi), got ${total_storage_gb}Gi"

# --- 24. A build-capable env (local-agent / remote-agent) gets the dind
# sidecar, its docker state PVC, and the socket volume; the sidecar declares
# explicit resource limits rather than inheriting whatever the namespace's
# LimitRange hands an unbounded container. The runtime container also reads
# the sidecar's real cpu/memory limit back via the downward API, so an
# in-pod build can see it without the config store the pod has no
# environment entry in. ---
for storage_args in "--set worktreeStorage=pvc --set worktreeRepoName=petios" "--set worktreeStorage=host --set-string worktreeHostPath=/host/git/petios"; do
    # shellcheck disable=SC2086
    rendered=$(render ${storage_args})
    names=$(container_names "${rendered}")
    [ "${names}" = "erun-devops
erun-dind" ] || fail "a build-capable env (${storage_args}) should render erun-devops + erun-dind, got: ${names}"

    grep -q '^kind: PersistentVolumeClaim$' "${rendered}" &&
        grep -q '^  name: test-docker$' "${rendered}" ||
        fail "a build-capable env (${storage_args}) should render the docker state PVC"

    grep -q '^        - name: docker-socket$' "${rendered}" ||
        fail "a build-capable env (${storage_args}) should render the docker-socket volume"

    dind_container "${rendered}" >"${dind_block}"
    [ -s "${dind_block}" ] || fail "a build-capable env (${storage_args}) should render the dind container"
    grep -q '^          resources:$' "${dind_block}" ||
        fail "the dind sidecar should declare explicit resources rather than inheriting a LimitRange default"
    grep -A3 '^          resources:$' "${dind_block}" | grep -q 'cpu:' ||
        fail "the dind sidecar should declare an explicit cpu limit"
    grep -A3 '^          resources:$' "${dind_block}" | grep -q 'memory:' ||
        fail "the dind sidecar should declare an explicit memory limit"

    runtime_block="${work_root}/runtime-dind-env.yaml"
    runtime_container "${rendered}" >"${runtime_block}"
    grep -A4 '^            - name: ERUN_DIND_CPU_LIMIT$' "${runtime_block}" | grep -q 'containerName: erun-dind' &&
        grep -A4 '^            - name: ERUN_DIND_CPU_LIMIT$' "${runtime_block}" | grep -q 'resource: limits.cpu' ||
        fail "the runtime container should read the dind sidecar's real cpu limit via the downward API"
    grep -A5 '^            - name: ERUN_DIND_MEMORY_LIMIT_MIB$' "${runtime_block}" | grep -q 'containerName: erun-dind' &&
        grep -A5 '^            - name: ERUN_DIND_MEMORY_LIMIT_MIB$' "${runtime_block}" | grep -q 'resource: limits.memory' &&
        grep -A5 '^            - name: ERUN_DIND_MEMORY_LIMIT_MIB$' "${runtime_block}" | grep -q 'divisor: 1Mi' ||
        fail "the runtime container should read the dind sidecar's real memory limit (in MiB) via the downward API"
done

# --- 25. A `type: runtime` env (worktreeStorage=none) never builds, so it gets
# no dind sidecar, no docker state PVC, no docker-socket volume, no binfmt
# installer, and no dind-group membership: none of it has anything to do on a
# pod that only ever installs a published version by reference. ---
rendered=$(render --set worktreeStorage=none)
names=$(container_names "${rendered}")
[ "${names}" = "erun-devops" ] ||
    fail "a runtime env should render only the erun-devops container, got: ${names}"

grep -q 'erun-dind' "${rendered}" &&
    fail "a runtime env should render no erun-dind reference at all"

grep -q '^  name: test-docker$' "${rendered}" &&
    fail "a runtime env should render no docker state PVC"

grep -q 'docker-socket' "${rendered}" &&
    fail "a runtime env should render no docker-socket volume or mount"

grep -q 'ERUN_DIND_CPU_LIMIT\|ERUN_DIND_MEMORY_LIMIT_MIB' "${rendered}" &&
    fail "a runtime env has no dind sidecar, so it should read no downward-API limit for one"

init_containers_section "${rendered}" >"${init_block}"
grep -q 'install-binfmt' "${init_block}" &&
    fail "a runtime env builds nothing, so it needs no binfmt installer"

pod_security_context "${rendered}" | grep -q 'supplementalGroups' &&
    fail "a runtime env has no docker daemon to join the group of"

# --- 26. Every container and init container the template can render declares
# both resources.limits and resources.requests. This is the structural version
# of #1061 (the dind sidecar) and #1076 (every init container): both bugs were
# "a container was added to the pod and nobody sized it", left to inherit
# whatever the namespace LimitRange defaults an unsized container to — which
# can equal the entire configured quota width. Checked across every
# envType/worktreeStorage permutation the template supports, so the next
# container added to the pod cannot reintroduce the pattern silently. ---
all_container_blocks() {
    awk '
        /^      (initContainers|containers):$/ { insec=1; next }
        /^      volumes:$/ { insec=0 }
        insec && /^        #/ { next }
        insec && /^        - image: /{ if (n>0) print "\f"; n++ }
        insec && n { print }
    ' "$1"
}

assert_every_container_sized() {
    file="$1"
    label="$2"
    all_container_blocks "${file}" | awk -v label="${label}" '
        BEGIN { RS="\f"; failed=0 }
        NF==0 { next }
        {
            name="(unnamed)"
            if (match($0, /\n          name: [^\n]+/)) {
                name = substr($0, RSTART+17, RLENGTH-17)
            }
            has_resources = ($0 ~ /\n          resources:\n/)
            has_limits = ($0 ~ /\n            limits:\n/)
            has_requests = ($0 ~ /\n            requests:\n/)
            if (!has_resources || !has_limits || !has_requests) {
                printf "FAIL: %s container %s is missing resources.limits/requests\n", label, name > "/dev/stderr"
                failed=1
            }
        }
        END { exit failed }
    ' || fail "${label}: a container renders without both resources.limits and resources.requests"
}

for storage_args in \
    "--set worktreeStorage=pvc --set worktreeRepoName=petios" \
    "--set worktreeStorage=host --set-string worktreeHostPath=/host/git/petios" \
    "--set worktreeStorage=none"; do
    # shellcheck disable=SC2086
    rendered=$(render ${storage_args})
    assert_every_container_sized "${rendered}" "storage(${storage_args})"
done

# --- 25. registryCredentialSecretName mounts the Secret `erun init` mints
# read-only on the runtime container, and renders nothing without a name --
# an env init never provisioned a credential for stays byte-for-byte
# unchanged. ---
rendered=$(render)
grep -q 'name: registry-credential' "${rendered}" &&
    fail "no registry-credential volume or mount should render without a secret name"

rendered=$(render --set-string registryCredentialSecretName=team-devops-registry-credential)
runtime_block="${work_root}/registry-credential-runtime.yaml"
runtime_container "${rendered}" >"${runtime_block}"
grep -A2 '^            - name: registry-credential$' "${runtime_block}" | grep -q 'mountPath: "/etc/erun/registry-credential"' ||
    fail "the registry credential mount belongs on the runtime container at /etc/erun/registry-credential"
grep -A2 '^            - name: registry-credential$' "${runtime_block}" | grep -q 'readOnly: true' ||
    fail "the registry credential mount should be read-only"

volume_block="${work_root}/registry-credential-volume.yaml"
awk '/^      volumes:/{f=1} f{print}' "${rendered}" >"${volume_block}"
grep -A5 '^        - name: registry-credential$' "${volume_block}" | grep -q 'secretName: "team-devops-registry-credential"' ||
    fail "the registry credential volume should name the secret erun init minted"
grep -A5 '^        - name: registry-credential$' "${volume_block}" | grep -q '^            optional: true$' ||
    fail "the registry credential volume should be optional, so a deploy that races ahead of the secret's own apply still starts"
grep -A5 '^        - name: registry-credential$' "${volume_block}" | grep -q 'key: ".dockerconfigjson"' ||
    fail "the registry credential volume should project the .dockerconfigjson key"

# --- The dind sidecar starts through the MTU-deriving wrapper, and the
# chart's own dockerd args still ride behind it. The wrapper's resolution
# logic is covered directly in erun-devops-dind-entrypoint_test.sh; what is
# checked here is only that the chart actually embeds and invokes it, since a
# chart that silently stopped doing so would put every build back on a 1500
# bridge the pod network cannot carry. ---
rendered=$(render --set clusterRegistryInsecure=10.1.2.3:5000)
dind_block="${work_root}/dind-entrypoint.yaml"
dind_container "${rendered}" >"${dind_block}"
grep -q 'exec dockerd-entrypoint.sh' "${dind_block}" ||
    fail "the dind sidecar should start through the wrapper, which execs the stock entrypoint"
grep -q -- '--mtu=' "${dind_block}" ||
    fail "the embedded wrapper should pass a derived --mtu to dockerd"
grep -q '^            - erun-dind$' "${dind_block}" ||
    fail "the wrapper needs a \$0 placeholder so the chart's args still arrive as \$1.."
grep -A2 '^          args:$' "${dind_block}" | grep -q -- '--insecure-registry' ||
    fail "the insecure-registry arg should survive the entrypoint wrapper"

# A runtime env renders no sidecar at all, so it must not carry the wrapper
# either -- the same gate every other dind-related block sits behind.
rendered=$(render --set worktreeStorage=none)
grep -q 'dockerd-entrypoint.sh' "${rendered}" &&
    fail "a runtime env builds nothing, so it needs no dind entrypoint wrapper"

# --- 12. A configured gateway routes the env through it, on any cloud provider ---
# Gateway routing is not AWS routing: an operator's gateway replaces the model
# provider wherever the env runs, so these render for a non-AWS env too.
rendered=$(render \
    --set-string claude.openRouterBaseURL=https://openrouter.ai/api \
    --set-string 'claude.openRouterAvailableModels=anthropic/claude-fable-5.1\,deepseek/deepseek-v4.1-flash' \
    --set-string claude.openRouterAuthTokenSecret=erun-claude-gateway \
    --set-string claude.openRouterModel=deepseek/deepseek-v4.1-flash \
    --set-string claude.openRouterModelContext=1048576)
grep -A1 '^            - name: ANTHROPIC_BASE_URL$' "${rendered}" | grep -q '"https://openrouter.ai/api"' ||
    fail "a configured gateway base URL should reach ANTHROPIC_BASE_URL on any provider"
grep -A1 '^            - name: ANTHROPIC_API_KEY$' "${rendered}" | grep -q '""' ||
    fail "the API key must be present and empty so Claude Code cannot fall back to a direct provider"
grep -A1 '^            - name: CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY$' "${rendered}" | grep -q '"1"' ||
    fail "gateway model discovery should be enabled so the catalog is selectable"
grep -A1 '^            - name: ERUN_CLAUDE_AVAILABLE_MODELS$' "${rendered}" | grep -q 'anthropic/claude-fable-5.1' ||
    fail "the catalog's models should reach ERUN_CLAUDE_AVAILABLE_MODELS"
# The model and its window are set pod-wide, not only at launch: Claude invoked
# outside the AI tab would otherwise fall back to a model the gateway need not
# serve, and would assume a window for an id it cannot size.
grep -A1 '^            - name: ANTHROPIC_MODEL$' "${rendered}" | grep -q '"deepseek/deepseek-v4.1-flash"' ||
    fail "the catalog's default model should be set pod-wide"
grep -A1 '^            - name: CLAUDE_CODE_MAX_CONTEXT_TOKENS$' "${rendered}" | grep -q '"1048576"' ||
    fail "the default model's context window should be set pod-wide"

# The credential travels as a Secret reference; no value reaches the manifest.
grep -A3 '^            - name: ANTHROPIC_AUTH_TOKEN$' "${rendered}" | grep -q 'secretKeyRef' ||
    fail "the gateway credential must be a Secret reference, not a value"
grep -A4 '^            - name: ANTHROPIC_AUTH_TOKEN$' "${rendered}" | grep -q 'name: "erun-claude-gateway"' ||
    fail "the credential Secret name should be rendered"
grep -A3 '^            - name: ANTHROPIC_AUTH_TOKEN$' "${rendered}" | grep -q '^              value:' &&
    fail "the credential must never render as a literal value"

# --- 13. An env with no gateway is unchanged ---
rendered=$(render)
for name in ANTHROPIC_BASE_URL ANTHROPIC_AUTH_TOKEN CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY; do
    grep -q "^            - name: ${name}\$" "${rendered}" &&
        fail "no gateway variables should render without a configured gateway (${name})"
done

# --- 14. The available-models variable renders exactly once, whichever path sets it ---
# Emitting it twice would leave the container spec ambiguous about which wins.
rendered=$(render \
    --set-string cloudContext.provider=aws \
    --set-string claude.openRouterBaseURL=https://openrouter.ai/api \
    --set-string claude.openRouterAvailableModels=anthropic/claude-fable-5.1)
count=$(grep -c '^            - name: ERUN_CLAUDE_AVAILABLE_MODELS$' "${rendered}")
[ "${count}" = "1" ] ||
    fail "ERUN_CLAUDE_AVAILABLE_MODELS should render exactly once with a gateway on an AWS env, got ${count}"

# --- 15. The build's cache bound and the docker claim describe the same volume ---
# The byte count the build bounds this environment's BuildKit cache by is only a
# bound if it is the size of the volume that cache actually lives on. Two
# independently written numbers drift: the dind sidecar's image tag once shipped
# as a literal that disagreed with the image VERSION beside it, and the fix it
# carried was inert on every environment for a release because of exactly that.
# Derived from the rendered claim rather than restated, so a change that moves
# both sides together cannot pass.
docker_claim_gi() {
    awk '/^  name: test-docker$/{found=1}
         found && /^      storage: /{sub(/^      storage: /,""); sub(/Gi$/,""); print; exit}' "$1"
}

cache_bound_bytes() {
    grep -A1 '^            - name: ERUN_DOCKER_VOLUME_BYTES$' "$1" |
        sed -n 's/^              value: "\([0-9]*\)"$/\1/p'
}

rendered=$(render --set dockerVolumeGi=120)
claim_gi=$(docker_claim_gi "${rendered}")
[ "${claim_gi}" = "120" ] ||
    fail "the docker claim should render the configured volume (120Gi), got '${claim_gi}'"
bound=$(cache_bound_bytes "${rendered}")
[ "${bound}" = "$((claim_gi * 1073741824))" ] ||
    fail "the build's cache bound should be the docker claim's own size (${claim_gi}Gi = $((claim_gi * 1073741824)) bytes), got '${bound}'"

# A runtime env runs no dind sidecar and has no docker claim, so there is no
# volume to bound and nothing should claim otherwise.
rendered=$(render --set worktreeStorage=none)
[ -z "$(cache_bound_bytes "${rendered}")" ] ||
    fail "no cache bound should render for an env with no docker volume"

echo "PASS: erun-devops chart pod shape"
