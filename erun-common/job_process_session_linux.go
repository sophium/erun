//go:build linux

package eruncommon

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// platformSessionProcesses builds this host's process table from /proc, where
// both halves of what the session scan needs are always available: field 3 of
// /proc/<pid>/stat is the state and field 6 is the session. Reading them is
// also what makes the scan independent of ps here -- the session column ps
// populates on Linux is empty on Darwin, and this is the source that answers
// on both.
func platformSessionProcesses() ([]sessionProcess, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	procs := make([]sessionProcess, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			// Exited while the table was being read, or not visible under a
			// hidepid mount. Either way there is nothing to compare.
			continue
		}
		proc, ok := parseProcStat(stat)
		if !ok || proc.pid != pid {
			// The entry no longer describes the pid that was listed, so the
			// pid may have been reused since; it is not this row's process.
			continue
		}
		procs = append(procs, proc)
	}
	return procs, true
}

// parseProcStat reads the pid, state, parent and session out of one
// /proc/<pid>/stat. The command name is the only field that may itself contain
// spaces and parentheses, so the pid is everything before the first '(' and
// the fields that follow are everything after the last ')': state first, then
// ppid, process group, and session.
func parseProcStat(stat []byte) (sessionProcess, bool) {
	open := bytes.IndexByte(stat, '(')
	closing := bytes.LastIndexByte(stat, ')')
	if open < 0 || closing < open || closing+1 >= len(stat) {
		return sessionProcess{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(stat[:open])))
	if err != nil {
		return sessionProcess{}, false
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) < 4 {
		return sessionProcess{}, false
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return sessionProcess{}, false
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return sessionProcess{}, false
	}
	return sessionProcess{pid: pid, zombie: fields[0] == "Z", session: session, parent: parent}, true
}
