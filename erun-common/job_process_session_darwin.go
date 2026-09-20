//go:build darwin

package eruncommon

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// platformSessionProcesses builds this host's process table. ps supplies the
// pids and their states -- columns that are sound here -- and getsid(2)
// supplies the session each process is in.
//
// The session cannot come from ps's own sess column on Darwin, nor from the
// sysctl table ps reads to fill it: the kernel's user-visible kinfo_proc has no
// member to carry a session id (e_sess is left as a kernel pointer), so
// `ps -o sess` prints 0 for every process, and a scan keyed on that column
// reports a job that backgrounded work into a fresh process group as having
// left nothing behind. getsid(2) is the source that can answer: Darwin looks
// the named pid up and returns its session id without the same-session
// restriction POSIX permits and Linux enforces, so it works for a session the
// caller is not in.
func platformSessionProcesses() ([]sessionProcess, bool) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,pgid=,stat=").Output()
	if err != nil {
		return nil, false
	}
	procs := parsePSProcessTable(out)
	for index, proc := range procs {
		sid, err := syscall.Getsid(proc.pid)
		if err != nil {
			// Exited between the two reads; the next pass of the scan's settle
			// loop sees it gone rather than reading a session it never had.
			continue
		}
		procs[index].session = sid
	}
	return procs, true
}

// parsePSProcessTable reads ps's pid, ppid, pgid and stat columns into a table
// whose session ids the caller fills in. Unlike the session column, ppid and
// pgid are real columns here, so they need no second source. A row whose pid,
// ppid or pgid will not parse is dropped rather than guessed at.
func parsePSProcessTable(out []byte) []sessionProcess {
	var procs []sessionProcess
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
		procs = append(procs, sessionProcess{pid: pid, parent: parent, group: group, zombie: psStatIsZombie(fields[3])})
	}
	return procs
}
