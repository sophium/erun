#!/bin/sh

# session-prune removes persistent desktop session sockets that no longer have a
# dtach server behind them.
#
# A dtach server is a process inside this container, so it can never outlive the
# container it ran in. Any socket present at boot is therefore a leftover: the
# desktop lists the session directory to decide which tabs to rebuild, and a
# socket with no server behind it makes it rebuild a tab that attaches to
# nothing. Pruning at boot is what makes the directory an honest statement of
# what is running, rather than a record of what used to be.
#
# This is also why the session directory stays container-local rather than
# moving onto the home PVC: persisting sockets across a pod replacement would
# preserve exactly the leftovers this removes, and `dtach -A` would then be
# attaching to dead sockets instead of creating fresh sessions.
#
# Deliberately conservative — a socket whose server IS alive is left alone — so
# the helper is safe to run at any point in the pod's life, not only at boot.

set -eu

session_dir="${1:-}"

if [ -z "${session_dir}" ]; then
    echo "usage: session-prune <session-dir>" >&2
    exit 2
fi

# Nothing created yet: no sessions have ever run in this container.
[ -d "${session_dir}" ] || exit 0

# session_has_live_server <socket> — true when a dtach process still references
# this socket. /proc-based for the same reason the rest of the session plumbing
# is: the runtime image ships no ss/lsof.
session_has_live_server() {
    for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do
        if grep -qF "$1" "/proc/${dtach_pid}/cmdline" 2>/dev/null; then
            return 0
        fi
    done
    return 1
}

for socket in "${session_dir}"/*.dtach; do
    [ -e "${socket}" ] || continue
    if session_has_live_server "${socket}"; then
        continue
    fi
    rm -f "${socket}" "${socket%.dtach}.owner"
    echo "erun: pruned stale session socket ${socket}" >&2
done
