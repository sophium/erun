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

container_names() {
    containers_section "$1" | sed -n 's/^          name: \(.*\)$/\1/p'
}

# The first container entry, i.e. the runtime container.
runtime_container() {
    containers_section "$1" | awk '/^        - image: /{c++} c==1'
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

echo "PASS: erun-devops chart pod shape"
