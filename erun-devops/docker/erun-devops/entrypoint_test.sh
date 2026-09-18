#!/bin/sh

# Tests for the entrypoint's MCP wiring: the runtime (devops) path starts the
# edge, the supervisor restarts a crashed server, an explicitly disabled edge
# starts nothing, and the standalone mcp command still serves the same server
# with the same flags plus its own pass-through arguments.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
entrypoint="${script_dir}/entrypoint.sh"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t entrypoint-test)"
run_pid=""
trap 'stop_run; rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# The devops path ends in `sleep infinity` and leaves the MCP supervisor running
# behind it, so each run gets its own session and is torn down by group signal.
stop_run() {
    [ -n "${run_pid}" ] || return 0
    kill -TERM "-${run_pid}" 2>/dev/null || true
    wait "${run_pid}" 2>/dev/null || true
    run_pid=""
}

# wait_for polls a condition rather than sleeping a fixed interval, so the test
# never races the entrypoint's boot steps or the supervisor's restart delay.
wait_for() {
    _deadline=$(( $(date +%s) + 30 ))
    while [ "$(date +%s)" -lt "${_deadline}" ]; do
        if eval "$1"; then
            return 0
        fi
        sleep 0.2
    done
    return 1
}

# Each run gets a fresh HOME and a stub PATH: `emcp` records its argv and pid and
# then stays alive until killed, and `erun` absorbs the entrypoint's activity
# calls. `exec` keeps the recorded pid valid for the long-lived process.
prepare_run() {
    run_dir="${work_root}/$1"
    rm -rf "${run_dir}"
    mkdir -p "${run_dir}/home" "${run_dir}/bin"

    cat >"${run_dir}/bin/emcp" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${run_dir}/emcp-argv"
printf '%s\n' "\$\$" >>"${run_dir}/emcp-pids"
exec sleep 300
EOF
    cat >"${run_dir}/bin/erun" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${run_dir}/erun-argv"
exit 0
EOF
    chmod +x "${run_dir}/bin/emcp" "${run_dir}/bin/erun"
    log="${run_dir}/log"
    : >"${log}"
}

# start_run <mcp-enabled> <entrypoint-arg>… — setsid puts the run in its own
# session so stop_run reaches the backgrounded supervisor too.
start_run() {
    _enabled="$1"
    shift
    env -i \
        HOME="${run_dir}/home" \
        PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
        ERUN_TENANT=team \
        ERUN_ENVIRONMENT=dev \
        ERUN_MCP_PORT=17000 \
        ERUN_MCP_ENABLED="${_enabled}" \
        ERUN_APP_SESSION_DIR="${session_dir_override:-}" \
        ANTHROPIC_BASE_URL="${anthropic_base_url_override:-}" \
        ANTHROPIC_MODEL="${anthropic_model_override:-}" \
        CLAUDE_CODE_MAX_CONTEXT_TOKENS="${claude_max_context_override:-}" \
        ERUN_CLAUDE_AVAILABLE_MODELS="${claude_available_models_override:-}" \
        setsid sh "${entrypoint}" "$@" >"${log}" 2>&1 &
    run_pid=$!
}

booted() {
    pgrep -g "${run_pid}" -f 'sleep infinity' >/dev/null 2>&1
}

# --- 1. Enabled: the runtime path serves the edge with the shared flags ---
prepare_run enabled
start_run true devops
wait_for '[ -s "${run_dir}/emcp-argv" ]' || fail "the devops path should start emcp when the edge is enabled"
argv=$(head -n 1 "${run_dir}/emcp-argv")
for flag in "--host 0.0.0.0" "--port 17000" "--path /mcp" "--metrics-host 0.0.0.0" "--metrics-port 9100" "--metrics-enabled=true" "--tenant team" "--environment dev" "--repo-path" "--kubernetes-context in-cluster"; do
    case "${argv}" in
        *"${flag}"*) ;;
        *) fail "emcp argv is missing '${flag}': ${argv}" ;;
    esac
done
grep -q 'starting erun MCP on 0.0.0.0:17000/mcp, metrics on 0.0.0.0:9100 (enabled=true)' "${log}" ||
    fail "the edge start and metrics listener should be logged"

# A space-separated bool flag (e.g. `--metrics-enabled true`) sets the bool from
# its own presence and leaves the bare "true" as the first positional argument,
# which stops Go's flag.Parse there — every flag after it (including --tenant
# and --environment) is silently dropped. Walk the captured argv the same way
# emcp's flag set would and fail if any bare positional token would stop
# parsing before every flag, including the ones after --metrics-enabled, is
# consumed.
assert_argv_parses_through() {
    _argv="$1"
    # shellcheck disable=SC2086
    set -- ${_argv}
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --metrics-enabled)
                fail "bool flag --metrics-enabled must be written as --metrics-enabled=<value>, not space-separated: ${_argv}"
                ;;
            --metrics-enabled=*)
                shift
                ;;
            --host | --port | --path | --metrics-host | --metrics-port | --tenant | --environment | --repo-path | --kubernetes-context | --namespace)
                shift 2
                ;;
            --*)
                shift
                ;;
            *)
                fail "unparsed positional argument '$1' would stop emcp's flag.Parse before later flags are applied: ${_argv}"
                ;;
        esac
    done
}
assert_argv_parses_through "${argv}"

# --- 2. Supervised: killing the server restarts it and logs the restart ---
first_pid=$(head -n 1 "${run_dir}/emcp-pids")
[ -n "${first_pid}" ] || fail "expected a running emcp process to kill"
kill -KILL "${first_pid}" 2>/dev/null || true
wait_for '[ "$(wc -l <"${run_dir}/emcp-argv")" -ge 2 ]' ||
    fail "the supervisor should restart a crashed emcp"
grep -q 'erun MCP exited; restarting (attempt 2)' "${log}" ||
    fail "a restart should be logged so a crash-loop stays visible"
stop_run

# --- 3. Disabled: an explicitly disabled edge starts nothing ---
prepare_run disabled
start_run false devops
wait_for booted || fail "the devops path should reach its idle foreground"
[ -f "${run_dir}/emcp-argv" ] && fail "a disabled edge must not start emcp"
stop_run

# --- 4. Standalone: the mcp command serves the same server, plus its own args ---
prepare_run standalone
start_run "" mcp --allow-tool raw
wait_for '[ -s "${run_dir}/emcp-argv" ]' || fail "the mcp command should start emcp"
standalone_argv=$(head -n 1 "${run_dir}/emcp-argv")
# Each run gets its own HOME, which the repo path derives from; collapse it so
# the two paths' argv can be compared for equality.
strip_home() {
    printf '%s\n' "$1" | sed "s#${work_root}/[a-z]*/home#<HOME>#"
}
[ "$(strip_home "${standalone_argv}")" = "--allow-tool raw $(strip_home "${argv}")" ] ||
    fail "standalone argv should be the pass-through args plus the shared flags: ${standalone_argv}"
stop_run

# --- 5. Boot reconciles stale session sockets; an in-container shell must not ---
# A dtach server cannot outlive its container, so every socket present at
# container start is a leftover the desktop would otherwise read as a running
# session. The `shell` path runs inside a live container, where the sockets are
# real, so it must never prune.
prepare_run prune
session_dir_override="${run_dir}/sessions"
mkdir -p "${session_dir_override}"
cat >"${run_dir}/bin/erun-prune-sessions" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${run_dir}/prune-argv"
EOF
chmod +x "${run_dir}/bin/erun-prune-sessions"

start_run true devops
wait_for '[ -s "${run_dir}/prune-argv" ]' ||
    fail "the devops boot path should reconcile the session directory"
[ "$(cat "${run_dir}/prune-argv")" = "${session_dir_override}" ] ||
    fail "the prune should target the session directory: $(cat "${run_dir}/prune-argv")"
stop_run

env -i \
    HOME="${run_dir}/home" \
    PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
    ERUN_TENANT=team \
    ERUN_ENVIRONMENT=dev \
    ERUN_APP_SESSION_DIR="${session_dir_override}" \
    sh "${entrypoint}" shell </dev/null >>"${log}" 2>&1 || true
[ "$(wc -l <"${run_dir}/prune-argv")" -eq 1 ] ||
    fail "an in-container shell must not prune live session sockets"
session_dir_override=""

# --- 6. The environment monitor samples resident work in every pod ---
# The sampler is what makes uninstrumented work — a build, a test suite, an
# agent nobody wrapped in a lease — register as activity. It must run in every
# pod, not only a cloud-managed one, because the desktop reads the same signal.
prepare_run sampler
start_run true devops
wait_for 'grep -q "^activity sample --tenant team --environment dev$" "${run_dir}/erun-argv" 2>/dev/null' ||
    fail "the environment monitor should sample resident work at boot: $(cat "${run_dir}/erun-argv" 2>/dev/null)"
stop_run

# --- 7. Registry credential sync merges the mounted Secret into
# ~/.docker/config.json at boot, seeding a missing host but leaving an
# unrelated existing host untouched ---
prepare_run registry_credential_merge
credential_src="${run_dir}/registry-credential.json"
cat >"${credential_src}" <<'JSON'
{"auths":{"ghcr.io":{"auth":"aGVsbG86d29ybGQ="}}}
JSON
mkdir -p "${run_dir}/home/.docker"
cat >"${run_dir}/home/.docker/config.json" <<'JSON'
{"auths":{"docker.io":{"auth":"ZXhpc3Rpbmc6dG9rZW4="}}}
JSON
env -i \
    HOME="${run_dir}/home" \
    PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
    ERUN_TENANT=team \
    ERUN_ENVIRONMENT=dev \
    ERUN_MCP_PORT=17000 \
    ERUN_MCP_ENABLED=true \
    ERUN_REGISTRY_CREDENTIAL_SRC_OVERRIDE="${credential_src}" \
    setsid sh "${entrypoint}" devops >"${run_dir}/log" 2>&1 &
run_pid=$!
wait_for 'grep -q ghcr.io "${run_dir}/home/.docker/config.json" 2>/dev/null' ||
    fail "the mounted registry credential should be merged into ~/.docker/config.json"
config=$(cat "${run_dir}/home/.docker/config.json")
case "${config}" in
    *'"docker.io"'*'"ZXhpc3Rpbmc6dG9rZW4="'*) ;;
    *) fail "an unrelated existing docker config entry must survive the merge: ${config}" ;;
esac
case "${config}" in
    *'"ghcr.io"'*'"aGVsbG86d29ybGQ="'*) ;;
    *) fail "the provisioned ghcr.io credential should be merged in: ${config}" ;;
esac
stop_run

# --- 8. Registry credential sync never overwrites a host entry the pod
# already has -- an operator's own docker login (or gh-driven push-recovery)
# is more current than what erun resolved on the host at init time ---
prepare_run registry_credential_preserve
credential_src="${run_dir}/registry-credential.json"
cat >"${credential_src}" <<'JSON'
{"auths":{"ghcr.io":{"auth":"cHJvdmlzaW9uZWQ6dG9rZW4="}}}
JSON
mkdir -p "${run_dir}/home/.docker"
cat >"${run_dir}/home/.docker/config.json" <<'JSON'
{"auths":{"ghcr.io":{"auth":"b3BlcmF0b3I6dG9rZW4="}}}
JSON
env -i \
    HOME="${run_dir}/home" \
    PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
    ERUN_TENANT=team \
    ERUN_ENVIRONMENT=dev \
    ERUN_MCP_PORT=17000 \
    ERUN_MCP_ENABLED=true \
    ERUN_REGISTRY_CREDENTIAL_SRC_OVERRIDE="${credential_src}" \
    setsid sh "${entrypoint}" devops >"${run_dir}/log" 2>&1 &
run_pid=$!
wait_for booted || fail "the devops path should reach its idle foreground"
config=$(cat "${run_dir}/home/.docker/config.json")
case "${config}" in
    *'"ghcr.io"'*'"b3BlcmF0b3I6dG9rZW4="'*) ;;
    *) fail "the pod's own existing credential must not be overwritten by the provisioned one: ${config}" ;;
esac
case "${config}" in
    *cHJvdmlzaW9uZWQ6dG9rZW4=*) fail "the provisioned credential must not replace an existing host entry: ${config}" ;;
    *) ;;
esac
stop_run

# --- 8a. The cloud-context defaults the entrypoint emits match the Go path ---
# entrypoint.sh re-derives in shell the cloud-context name/kubernetes-context
# fallback that erun-common owns (ResolveInjectedRuntimeConfig routing through
# NormalizeCloudContextConfig). Nothing asserted the two agreed, which is how
# #1662 stayed silent -- doctor --sync-config reported phantom drift on every
# run, never reached InSync, and no test went red. Both sides read
# cloud_context_defaults.tsv, so editing one fallback without the other turns
# the other's test red (the Go twin is
# erun-common/cloud_context_entrypoint_parity_test.go).
defaults_fixture="${script_dir}/cloud_context_defaults.tsv"
[ -f "${defaults_fixture}" ] || fail "the shared cloud-context fixture is missing: ${defaults_fixture}"
defaults_cases=0
while IFS="$(printf '\t')" read -r label want_name want_kube expected_name expected_kube; do
    case "${label}" in '' | '#'*) continue ;; esac
    defaults_cases=$((defaults_cases + 1))
    prepare_run "cloudctx_${label}"
    run_dir="${work_root}/cloudctx_${label}"

    # "-" is the fixture's "unset", so the variable is omitted entirely rather
    # than passed empty -- an empty value is not the same input state here.
    cloud_context_env=""
    [ "${want_name}" = "-" ] || cloud_context_env="ERUN_CLOUD_CONTEXT_NAME=${want_name}"
    kubernetes_context_env=""
    [ "${want_kube}" = "-" ] || kubernetes_context_env="ERUN_KUBERNETES_CONTEXT=${want_kube}"

    # shellcheck disable=SC2086 # the two vars must word-split away when unset
    env -i \
        HOME="${run_dir}/home" \
        PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
        ERUN_TENANT=team \
        ERUN_ENVIRONMENT=dev \
        ERUN_MCP_PORT=17000 \
        ERUN_MCP_ENABLED=false \
        ERUN_CLOUD_PROVIDER=aws \
        ERUN_CLOUD_PROVIDER_ALIAS=operator@aws \
        ERUN_CLOUD_REGION=us-east-1 \
        ${cloud_context_env} ${kubernetes_context_env} \
        setsid sh "${entrypoint}" devops >"${run_dir}/log" 2>&1 &
    run_pid=$!
    wait_for booted || fail "the devops path should reach its idle foreground"

    emitted=$(sed -n '/^cloudcontexts:/,/^[a-z]/p' "${run_dir}/home/.config/erun/config.yaml")
    case "${emitted}" in
        *"  - name: ${expected_name}"*) ;;
        *) fail "${label}: the emitted cloud context name should be '${expected_name}', matching the Go normalizer: ${emitted}" ;;
    esac
    case "${emitted}" in
        *"kubernetescontext: ${expected_kube}"*) ;;
        *) fail "${label}: the emitted kubernetescontext should be '${expected_kube}', matching the Go normalizer: ${emitted}" ;;
    esac
    stop_run
done <"${defaults_fixture}"
[ "${defaults_cases}" -gt 0 ] || fail "the shared cloud-context fixture yielded no cases"
stop_run

# --- 9. A configured gateway relays Claude Code's routing settings ---
# The gateway's address and credential reach the container as environment
# variables, but two things have to land in Claude Code's settings file: the
# model list, which is what makes the catalog selectable, and the model's
# context window, because Claude Code assumes one for an id it cannot size.
# The credential is deliberately absent — it stays in the pod environment via
# its Secret reference, so it never reaches a settings file erun wrote.
prepare_run gateway
run_dir="${work_root}/gateway"
env -i \
    HOME="${run_dir}/home" \
    PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
    ERUN_TENANT=team \
    ERUN_ENVIRONMENT=dev \
    ERUN_MCP_PORT=17000 \
    ERUN_MCP_ENABLED=false \
    ANTHROPIC_BASE_URL=https://openrouter.ai/api \
    ANTHROPIC_MODEL=deepseek/deepseek-v4.1-flash \
    CLAUDE_CODE_MAX_CONTEXT_TOKENS=1048576 \
    ERUN_CLAUDE_AVAILABLE_MODELS='anthropic/claude-fable-5.1,deepseek/deepseek-v4.1-flash' \
    setsid sh "${entrypoint}" devops >"${run_dir}/log" 2>&1 &
run_pid=$!
wait_for booted || fail "the devops path should reach its idle foreground"
settings=$(cat "${run_dir}/home/.claude/settings.json")
case "${settings}" in
    *'claude-fable-5.1'*) ;;
    *) fail "the catalog's models should reach the settings model list: ${settings}" ;;
esac
case "${settings}" in
    *'deepseek/deepseek-v4.1-flash'*) ;;
    *) fail "the catalog's default model should be relayed into settings: ${settings}" ;;
esac
case "${settings}" in
    *'1048576'*) ;;
    *) fail "the model's context window should be relayed into settings: ${settings}" ;;
esac
case "${settings}" in
    *ANTHROPIC_AUTH_TOKEN*) fail "the credential must never be written into a settings file: ${settings}" ;;
    *) ;;
esac
stop_run

# --- 10. Without a gateway the relay writes no routing values ---
# An install that has configured no gateway must land in exactly the settings
# shape it did before, or every env without one changes behaviour.
prepare_run no_gateway
run_dir="${work_root}/no_gateway"
env -i \
    HOME="${run_dir}/home" \
    PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
    ERUN_TENANT=team \
    ERUN_ENVIRONMENT=dev \
    ERUN_MCP_PORT=17000 \
    ERUN_MCP_ENABLED=false \
    setsid sh "${entrypoint}" devops >"${run_dir}/log" 2>&1 &
run_pid=$!
wait_for booted || fail "the devops path should reach its idle foreground"
settings=$(cat "${run_dir}/home/.claude/settings.json")
for name in ANTHROPIC_BASE_URL ANTHROPIC_MODEL CLAUDE_CODE_MAX_CONTEXT_TOKENS; do
    case "${settings}" in
        *"${name}"*) fail "no ${name} should be relayed without a gateway: ${settings}" ;;
        *) ;;
    esac
done
stop_run

echo "PASS: entrypoint MCP supervision, session reconciliation, activity sampling, registry credential sync, gateway settings relay, and cloud-context default parity with the Go normalizer"
