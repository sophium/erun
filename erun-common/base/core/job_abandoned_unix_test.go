//go:build !windows

package core

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killProcessesMatching kills every process whose command line names path,
// for a background process that escaped the job's own process group (so
// signalEnvironmentJobProcessGroup on the job's ChildPID cannot reach it), and
// then waits for them to go.
//
// The wait is what keeps one test's leftover out of the next test's: a
// survivor of a job is deliberately detached, and the supervisor is a child
// subreaper, so an escaped leftover is reparented onto the test process and
// stays visible to every descendant scan the tests below run until it actually
// dies. Signalling it is not the same as it being gone.
//
// A leftover only answers to this if its own command line carries path:
// `sleep 5` redirected to a log names neither, which is why the background
// commands below give their process a marker to be found by.
func killProcessesMatching(path string) {
	deadline := time.Now().Add(EnvironmentJobProcessGroupSurvivorSettleWindow * 10)
	for {
		pids := pidsMatching(path)
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(EnvironmentJobProcessGroupSurvivorSettlePoll)
	}
}

// pidsMatching returns the pids whose command line names path, ignoring the
// ones pgrep cannot report.
func pidsMatching(path string) []int {
	out, err := exec.Command("pgrep", "-f", path).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
