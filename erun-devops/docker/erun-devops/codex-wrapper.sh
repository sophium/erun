#!/bin/sh

set -eu

touch_codex() {
    if [ -z "${ERUN_TENANT:-}" ] || [ -z "${ERUN_ENVIRONMENT:-}" ]; then
        return
    fi
    erun activity touch --tenant "${ERUN_TENANT}" --environment "${ERUN_ENVIRONMENT}" --kind codex "$@" >/dev/null 2>&1 || true
}

shell_quote() {
    printf "'"
    printf '%s' "$1" | sed "s/'/'\\\\''/g"
    printf "'"
}

monitor_codex_log() {
    log_file="${1}"
    previous_size=0
    seen_ticks=0
    while [ -f "${log_file}" ]; do
        sleep 5
        current_size=$(wc -c <"${log_file}" 2>/dev/null || printf '0')
        if [ "${current_size}" -gt "${previous_size}" ]; then
            delta=$((current_size - previous_size))
            previous_size="${current_size}"
            seen_ticks=0
            touch_codex --bytes "${delta}"
            continue
        fi
        previous_size="${current_size}"
        seen_ticks=$((seen_ticks + 1))
        if [ "${seen_ticks}" -ge 6 ]; then
            seen_ticks=0
            touch_codex --seen
        fi
    done
}

touch_codex

if command -v script >/dev/null 2>&1; then
    log_file=$(mktemp "${TMPDIR:-/tmp}/erun-codex-output.XXXXXX")
    runner_file=$(mktemp "${TMPDIR:-/tmp}/erun-codex-runner.XXXXXX")
    cat >"${runner_file}" <<'EOF'
#!/bin/sh
exec codex-real "$@"
EOF
    chmod 700 "${runner_file}"

    command=$(shell_quote "${runner_file}")
    for arg in "$@"; do
        command="${command} $(shell_quote "${arg}")"
    done

    monitor_codex_log "${log_file}" &
    monitor_pid=$!
    cleanup() {
        kill "${monitor_pid}" >/dev/null 2>&1 || true
        rm -f "${log_file}" "${runner_file}"
    }
    trap cleanup EXIT HUP INT TERM

    set +e
    script -q -f -e -c "${command}" "${log_file}"
    status=$?
    set -e
    exit "${status}"
fi

(
    while :; do
        sleep 30
        touch_codex --seen
    done
) &
heartbeat_pid=$!
cleanup() {
    kill "${heartbeat_pid}" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

codex-real "$@"
