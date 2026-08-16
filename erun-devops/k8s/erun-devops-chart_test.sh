#!/bin/sh

# Tests for the erun-devops runtime chart's pod shape: the runtime container is
# the environment's only long-lived application container and serves the MCP
# edge itself, the MCP auth env and key mount land on it, the runtime image
# override still applies, and a disabled edge renders no port.
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

echo "PASS: erun-devops chart pod shape"
