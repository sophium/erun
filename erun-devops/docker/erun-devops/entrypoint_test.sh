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
    cat >"${run_dir}/bin/erun" <<'EOF'
#!/bin/sh
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
for flag in "--host 0.0.0.0" "--port 17000" "--path /mcp" "--tenant team" "--environment dev" "--repo-path" "--kubernetes-context in-cluster"; do
    case "${argv}" in
        *"${flag}"*) ;;
        *) fail "emcp argv is missing '${flag}': ${argv}" ;;
    esac
done
grep -q 'starting erun MCP on 0.0.0.0:17000/mcp' "${log}" ||
    fail "the edge start should be logged"

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

echo "PASS: entrypoint MCP supervision"
