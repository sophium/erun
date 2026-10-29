package remotesession

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// A session is running only when its socket exists and a dtach master holds a live program;
// inferring it from stream traffic gets both directions wrong.

// RemoteAppSessionHeartbeat is one observed persistent desktop session.
type RemoteAppSessionHeartbeat struct {
	ID string
	// PID is the dtach master, 0 when none.
	PID     int
	Program string
}

// Running reports whether a live program is behind the socket; one without is a leftover.
func (h RemoteAppSessionHeartbeat) Running() bool {
	return h.PID > 0
}

const remoteAppSessionHeartbeatPrefix = "erun-session\t"

// RemoteAppSessionHeartbeatScript reports each session and whether it has a live program.
func RemoteAppSessionHeartbeatScript(tenant, environment string) string {
	return remoteAppSessionHeartbeatScriptIn(RemoteAppSessionSocketDir, tenant, environment)
}

func remoteAppSessionHeartbeatScriptIn(dir, tenant, environment string) string {
	prefix := core.SanitizeForFilename(tenant) + "-" + core.SanitizeForFilename(environment) + "-"
	lines := []string{
		fmt.Sprintf("for socket in \"%s/%s\"*.dtach; do", dir, prefix),
		"[ -S \"$socket\" ] || continue",
		fmt.Sprintf("name=\"${socket##*/}\"; id=\"${name#%s}\"; id=\"${id%%.dtach}\"", prefix),
	}
	lines = append(lines, RemoteAppSessionMasterScanLines("$socket")...)
	return strings.Join(append(lines,
		fmt.Sprintf("printf '%s%%s\\t%%s\\t%%s\\n' \"$id\" \"${master_pid:-0}\" \"${master_program}\"", remoteAppSessionHeartbeatPrefix),
		"done",
	), "\n")
}

// ParseRemoteAppSessionHeartbeats drops unparseable lines rather than failing.
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

// RunningRemoteAppSessions counts via Running so the total and per-session state cannot disagree.
func RunningRemoteAppSessions(heartbeats []RemoteAppSessionHeartbeat) int {
	running := 0
	for _, heartbeat := range heartbeats {
		if heartbeat.Running() {
			running++
		}
	}
	return running
}
