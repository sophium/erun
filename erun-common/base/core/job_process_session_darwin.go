//go:build darwin

package core

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// platformSessionProcesses takes pids and states from ps and sessions from
// getsid(2): Darwin's `ps -o sess` prints 0 for every process, and Darwin's
// getsid works across sessions.
func platformSessionProcesses() ([]SessionProcess, bool) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,pgid=,stat=").Output()
	if err != nil {
		return nil, false
	}
	procs := parsePSProcessTable(out)
	for index, proc := range procs {
		sid, err := syscall.Getsid(proc.Pid)
		if err != nil {
			// Exited between reads; the settle loop's next pass sees it gone.
			continue
		}
		procs[index].session = sid
	}
	return procs, true
}

// parsePSProcessTable reads pid, ppid, pgid and stat; sessions are filled in by
// the caller. Unparseable rows are dropped.
func parsePSProcessTable(out []byte) []SessionProcess {
	var procs []SessionProcess
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		procs = append(procs, SessionProcess{Pid: pid, Parent: parent, group: group, Zombie: psStatIsZombie(fields[3])})
	}
	return procs
}
