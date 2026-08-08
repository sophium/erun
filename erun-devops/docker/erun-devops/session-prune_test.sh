#!/bin/sh

# Tests for session-prune.sh: a socket with no dtach server behind it is removed
# along with its owner file, a socket whose server is alive is left alone,
# non-session files are untouched, and a missing directory is a clean no-op.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
pruner="${script_dir}/session-prune.sh"
if [ ! -x "${pruner}" ]; then
    chmod +x "${pruner}"
fi

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t session-prune-test)"
holder_pid=""
trap 'stop_holder; rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

stop_holder() {
    [ -n "${holder_pid}" ] || return 0
    kill "${holder_pid}" 2>/dev/null || true
    wait "${holder_pid}" 2>/dev/null || true
    holder_pid=""
}

sessions="${work_root}/erun-sessions"

reset_sessions() {
    rm -rf "${sessions}"
    mkdir -p "${sessions}"
    : >"${sessions}/team-dev-open-0.dtach"
    : >"${sessions}/team-dev-open-0.owner"
    : >"${sessions}/team-dev-ai.dtach"
    : >"${sessions}/keep-me.log"
}

# --- 1. Leftovers: no dtach server is running, so every socket is stale ---
reset_sessions
"${pruner}" "${sessions}"
[ ! -e "${sessions}/team-dev-open-0.dtach" ] || fail "expected the stale socket to be pruned"
[ ! -e "${sessions}/team-dev-open-0.owner" ] || fail "expected the stale owner file to be pruned with its socket"
[ ! -e "${sessions}/team-dev-ai.dtach" ] || fail "expected every stale socket to be pruned"
[ -e "${sessions}/keep-me.log" ] || fail "a non-session file must not be touched"

# --- 2. Live session: a socket whose dtach server is alive must survive ---
# A stub named `dtach` whose argv carries the socket path is exactly what the
# /proc scan looks for, so this exercises the real guard rather than a mock of it.
reset_sessions
stub_dir="${work_root}/bin"
mkdir -p "${stub_dir}"
# A real binary named `dtach` (so /proc/<pid>/comm matches `pgrep -x dtach`)
# whose argv carries the socket path (so the /proc/<pid>/cmdline scan matches).
cp /bin/sh "${stub_dir}/dtach"
"${stub_dir}/dtach" -c 'while :; do sleep 1; done' "${sessions}/team-dev-ai.dtach" &
holder_pid=$!
# Wait for the stub to be visible to pgrep before pruning, so the test never
# races process startup.
deadline=$(( $(date +%s) + 10 ))
while ! pgrep -x dtach 2>/dev/null | grep -qx "${holder_pid}"; do
    [ "$(date +%s)" -lt "${deadline}" ] || fail "dtach stub never became visible to pgrep"
    sleep 0.1
done

"${pruner}" "${sessions}"
[ -e "${sessions}/team-dev-ai.dtach" ] || fail "a socket with a live dtach server must be kept"
[ ! -e "${sessions}/team-dev-open-0.dtach" ] || fail "the socket with no server must still be pruned"
stop_holder

# --- 3. Absent directory: nothing has ever run here, so this is a no-op ---
rm -rf "${sessions}"
"${pruner}" "${sessions}" || fail "an absent session directory must exit clean"

# --- 4. Usage: a missing argument is an error, not a guess at a directory ---
if "${pruner}" >/dev/null 2>&1; then
    fail "expected a usage error without a session directory"
fi

echo "session-prune tests passed"
