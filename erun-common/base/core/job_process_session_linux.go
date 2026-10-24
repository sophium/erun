//go:build linux

package core

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// platformSessionProcesses builds the table from /proc/<pid>/stat (state,
// process group, session).
func platformSessionProcesses() ([]SessionProcess, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	procs := make([]SessionProcess, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			// Exited, or hidden by a hidepid mount.
			continue
		}
		proc, ok := parseProcStat(stat)
		if !ok || proc.Pid != pid {
			// The pid may have been reused since it was listed.
			continue
		}
		procs = append(procs, proc)
	}
	return procs, true
}

// parseProcStat reads /proc/<pid>/stat. The command name may contain spaces and
// parentheses, so fields are taken after the last ')'.
func parseProcStat(stat []byte) (SessionProcess, bool) {
	open := bytes.IndexByte(stat, '(')
	closing := bytes.LastIndexByte(stat, ')')
	if open < 0 || closing < open || closing+1 >= len(stat) {
		return SessionProcess{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(stat[:open])))
	if err != nil {
		return SessionProcess{}, false
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) < 4 {
		return SessionProcess{}, false
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return SessionProcess{}, false
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return SessionProcess{}, false
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return SessionProcess{}, false
	}
	return SessionProcess{Pid: pid, Zombie: fields[0] == "Z", session: session, group: group, Parent: parent}, true
}
