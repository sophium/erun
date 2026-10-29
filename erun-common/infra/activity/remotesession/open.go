package remotesession

import (
	"fmt"
)

// RemoteAppSessionSocketDir is pod-ephemeral because a dtach socket is useless without its
// process. The name must not contain the desktop binary's name, or `pkill -f erun-app`
// would hit live terminals.
const RemoteAppSessionSocketDir = "/tmp/erun-sessions"

// RemoteAppSessionMasterScanLines sets $master_pid and $master_program to the dtach process
// with a non-dtach child. It reads /proc (no ss/lsof in the image) and leaves $master_pid
// empty when unsure so callers fail open.
func RemoteAppSessionMasterScanLines(socket string) []string {
	return []string{
		"master_pid=\"\"",
		"master_program=\"\"",
		fmt.Sprintf("for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do if grep -qF \"%s\" \"/proc/$dtach_pid/cmdline\" 2>/dev/null; then for child_pid in $(pgrep -P \"$dtach_pid\" 2>/dev/null || true); do child_comm=\"$(cat \"/proc/$child_pid/comm\" 2>/dev/null)\"; if [ -n \"$child_comm\" ] && [ \"$child_comm\" != \"dtach\" ]; then master_pid=\"$dtach_pid\"; master_program=\"$child_comm\"; fi; done; fi; done", socket),
	}
}
