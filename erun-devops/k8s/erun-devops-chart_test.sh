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

echo "PASS: erun-devops chart pod shape"
