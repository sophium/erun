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
session_dir_override=""
agent_config_dir_override=""
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
        ERUN_AGENT_CONFIG_STATE_DIR="${agent_config_dir_override:-${run_dir}/agent-config}" \
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
    ERUN_AGENT_CONFIG_STATE_DIR="${run_dir}/agent-config" \
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
    ERUN_AGENT_CONFIG_STATE_DIR="${run_dir}/agent-config" \
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
    ERUN_AGENT_CONFIG_STATE_DIR="${run_dir}/agent-config" \
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

# --- 9. Agent MCP configuration is reconciled once per container boot, not once
# per shell ---
# Both configure scripts rewrite ~/.claude and ~/.codex from the image's baked
# skills/agents and the container's env — none of which move while the container
# lives — so re-running them from the shell hook charged every shell start (and
# every `sh -lc` remote exec) the full reconcile. Assert the structural property
# rather than a wall-clock bound: each configure script runs exactly once for a
# given container-lifetime state directory, however many shells source the hook.
prepare_run agent_config_once
cat >"${run_dir}/bin/erun-install-skills" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${run_dir}/install-skills-argv"
EOF
cat >"${run_dir}/bin/erun-install-agents" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${run_dir}/install-agents-argv"
EOF
cat >"${run_dir}/bin/node" <<EOF
#!/bin/sh
cat >/dev/null
printf 'ran\n' >>"${run_dir}/node-runs"
EOF
chmod +x "${run_dir}/bin/erun-install-skills" "${run_dir}/bin/erun-install-agents" "${run_dir}/bin/node"

boot_shell() {
    env -i \
        HOME="${run_dir}/home" \
        PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
        ERUN_TENANT=team \
        ERUN_ENVIRONMENT=dev \
        ERUN_AGENT_CONFIG_STATE_DIR="${run_dir}/agent-config" \
        sh "${entrypoint}" shell </dev/null >>"${log}" 2>&1 || true
}

# source_hook runs the installed hook the way a login shell does, in its own
# process, so nothing but the state directory carries between invocations.
source_hook() {
    env -i \
        HOME="${run_dir}/home" \
        PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
        ERUN_TENANT=team \
        ERUN_ENVIRONMENT=dev \
        ERUN_AGENT_CONFIG_STATE_DIR="${run_dir}/agent-config" \
        sh -c ". \"\${HOME}/.erun-shell-hook.bashrc\"" >/dev/null 2>&1 || true
}

count_lines() {
    [ -f "$1" ] || { echo 0; return; }
    wc -l <"$1" | tr -d ' '
}

boot_shell
[ -x "${run_dir}/home/.erun-shell-hook.bashrc" ] || [ -r "${run_dir}/home/.erun-shell-hook.bashrc" ] ||
    fail "the boot should install the shell hook"
skills_after_boot=$(count_lines "${run_dir}/install-skills-argv")
agents_after_boot=$(count_lines "${run_dir}/install-agents-argv")
[ "${skills_after_boot}" -eq 2 ] ||
    fail "boot should reconcile skills once per agent (codex + claude), got ${skills_after_boot}"
[ "${agents_after_boot}" -eq 1 ] ||
    fail "boot should reconcile agents exactly once, got ${agents_after_boot}"

source_hook
source_hook
source_hook
[ "$(count_lines "${run_dir}/install-skills-argv")" -eq "${skills_after_boot}" ] ||
    fail "a shell sourcing the hook must not re-run the configure scripts: $(count_lines "${run_dir}/install-skills-argv") skill installs after 3 shells"
[ "$(count_lines "${run_dir}/install-agents-argv")" -eq "${agents_after_boot}" ] ||
    fail "a shell sourcing the hook must not re-run the claude configure script"

# A second entrypoint invocation in the same container — every `erun open` is
# one — is the same boot, so it must not repay either.
boot_shell
[ "$(count_lines "${run_dir}/install-skills-argv")" -eq "${skills_after_boot}" ] ||
    fail "a second entrypoint run in the same container must not re-run the configure scripts"

# A fresh container (the state directory is container-lifetime) reconciles again,
# so a rebuilt image's skills still reach the pod.
rm -rf "${run_dir}/agent-config"
source_hook
[ "$(count_lines "${run_dir}/install-skills-argv")" -gt "${skills_after_boot}" ] ||
    fail "a fresh container must reconcile the agent configuration again"

# --- 10. An unreachable IMDS costs one timeout, not two ---
# The region probe only runs on AWS, and where the link-local address answers
# nothing it drains curl's whole timeout; the unauthenticated fallback can only
# help an IMDSv1-only instance, which refuses the token immediately instead of
# timing out. Paying both timeouts is what made this the single largest item in
# the profile.
cat >"${run_dir}/bin/curl" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${run_dir}/curl-argv"
exit 28
EOF
chmod +x "${run_dir}/bin/curl"
env -i \
    HOME="${run_dir}/home" \
    PATH="${run_dir}/bin:/usr/local/bin:/usr/bin:/bin" \
    ERUN_TENANT=team \
    ERUN_ENVIRONMENT=dev \
    ERUN_CLOUD_PROVIDER=aws \
    sh "${run_dir}/home/.erun/configure-claude-code.sh" >/dev/null 2>&1 || true
imds_calls=$(count_lines "${run_dir}/curl-argv")
[ "${imds_calls}" -eq 1 ] ||
    fail "an unreachable IMDS should cost one timeout, not ${imds_calls}: $(cat "${run_dir}/curl-argv")"
grep -q -- '--connect-timeout' "${run_dir}/curl-argv" ||
    fail "the IMDS probe should bound its connect phase: $(cat "${run_dir}/curl-argv")"

echo "PASS: entrypoint MCP supervision, session reconciliation, activity sampling, registry credential sync, and once-per-boot agent configuration"
