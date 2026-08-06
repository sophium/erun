package eruncommon

import (
	"fmt"
	"strconv"
	"strings"
)

// A persistent desktop session is only running when two things are true at the
// same time in the pod: its socket exists, and a dtach master still holds a
// live program behind it. Listing the sockets (ParseRemoteAppSessionIDs) answers
// the first; this file answers the second, so a client can tell a working
// session from an abandoned socket without inferring it from stream traffic.
// Inference is what goes wrong: a session that stops printing looks dead and a
// dropped exec stream looks alive, so a UI driven by the last output event can
// claim a count and an animation that contradict each other.

// RemoteAppSessionHeartbeat is one observed persistent desktop session.
// Running is the authoritative answer — a socket with no master behind it is a
// leftover, not a session.
type RemoteAppSessionHeartbeat struct {
	ID string
	// PID is the dtach master holding the session program, 0 when none does.
	PID int
	// Program names the master's child (`bash`, `claude`, …) so a client can say
	// what is running rather than only how many things are.
	Program string
}

// Running reports whether the socket still has a live program behind it.
func (h RemoteAppSessionHeartbeat) Running() bool {
	return h.PID > 0
}

// remoteAppSessionHeartbeatPrefix is the line tag the heartbeat script emits, so
// the parser ignores whatever the pod's shell prints around it.
const remoteAppSessionHeartbeatPrefix = "erun-session\t"

// RemoteAppSessionHeartbeatScript returns the sh script that reports this
// tenant+env's persistent desktop sessions and whether each still has a live
// program. /proc-based like the rest of the session plumbing: the runtime image
// ships no ss/lsof, and the socket alone cannot distinguish a working session
// from a leftover.
func RemoteAppSessionHeartbeatScript(tenant, environment string) string {
	return remoteAppSessionHeartbeatScriptIn(RemoteAppSessionSocketDir, tenant, environment)
}

func remoteAppSessionHeartbeatScriptIn(dir, tenant, environment string) string {
	prefix := sanitizeForFilename(tenant) + "-" + sanitizeForFilename(environment) + "-"
	lines := []string{
		fmt.Sprintf("for socket in \"%s/%s\"*.dtach; do", dir, prefix),
		"[ -S \"$socket\" ] || continue",
		fmt.Sprintf("name=\"${socket##*/}\"; id=\"${name#%s}\"; id=\"${id%%.dtach}\"", prefix),
	}
	lines = append(lines, remoteAppSessionMasterScanLines("$socket")...)
	return strings.Join(append(lines,
		fmt.Sprintf("printf '%s%%s\\t%%s\\t%%s\\n' \"$id\" \"${master_pid:-0}\" \"${master_program}\"", remoteAppSessionHeartbeatPrefix),
		"done",
	), "\n")
}

// ParseRemoteAppSessionHeartbeats reads RemoteAppSessionHeartbeatScript's
// output. Unparseable lines are dropped rather than failing the read: the
// caller uses this to decide what is running, and a garbled line must never
// make a live session disappear from the count as an error.
func ParseRemoteAppSessionHeartbeats(output string) []RemoteAppSessionHeartbeat {
	var out []RemoteAppSessionHeartbeat
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if !strings.HasPrefix(line, remoteAppSessionHeartbeatPrefix) {
			continue
		}
		fields := strings.Split(strings.TrimPrefix(line, remoteAppSessionHeartbeatPrefix), "\t")
		id := strings.TrimSpace(fields[0])
		if id == "" {
			continue
		}
		heartbeat := RemoteAppSessionHeartbeat{ID: id}
		if len(fields) > 1 {
			if pid, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil && pid > 0 {
				heartbeat.PID = pid
			}
		}
		if len(fields) > 2 {
			heartbeat.Program = strings.TrimSpace(fields[2])
		}
		out = append(out, heartbeat)
	}
	return out
}

// RunningRemoteAppSessions counts the observed sessions that actually have a
// live program, so a rendered "N sessions running" is the same observation the
// per-session running state is drawn from and the two cannot disagree.
func RunningRemoteAppSessions(heartbeats []RemoteAppSessionHeartbeat) int {
	running := 0
	for _, heartbeat := range heartbeats {
		if heartbeat.Running() {
			running++
		}
	}
	return running
}
