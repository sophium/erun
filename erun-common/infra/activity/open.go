package activity

import (
	"fmt"
)

// RemoteAppSessionSocketDir holds persistent desktop session sockets in the
// runtime pod. Pod-ephemeral on purpose: a dtach server is a process in the
// container, so a socket that outlived its pod could never be attached to —
// clearing them with the pod is what lets `claude --continue` resume the
// conversation in a fresh session instead of failing against a dead socket.
//
// The name must not contain the desktop binary's process name. Freeing that
// binary for a rebuild invites `pkill -f erun-app`, and a socket path carrying
// that string puts every operator's live terminal in the blast radius.
const RemoteAppSessionSocketDir = "/tmp/erun-sessions"

// RemoteAppSessionMasterScanLines emits the sh lines that resolve $master_pid
// and $master_program for the session socket: the dtach process with a
// non-dtach child (the session program), and that child's name. Clients have no
// children, and the -A creator's only child is the master itself. /proc-based
// on purpose: the runtime image ships no ss/lsof, and an unidentifiable master
// must leave $master_pid empty so callers fail open instead of killing the
// session. The same scan answers "is this session still running" for the
// heartbeat, so there is one definition of a session's master rather than two.
func RemoteAppSessionMasterScanLines(socket string) []string {
	return []string{
		"master_pid=\"\"",
		"master_program=\"\"",
		fmt.Sprintf("for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do if grep -qF \"%s\" \"/proc/$dtach_pid/cmdline\" 2>/dev/null; then for child_pid in $(pgrep -P \"$dtach_pid\" 2>/dev/null || true); do child_comm=\"$(cat \"/proc/$child_pid/comm\" 2>/dev/null)\"; if [ -n \"$child_comm\" ] && [ \"$child_comm\" != \"dtach\" ]; then master_pid=\"$dtach_pid\"; master_program=\"$child_comm\"; fi; done; fi; done", socket),
	}
}
