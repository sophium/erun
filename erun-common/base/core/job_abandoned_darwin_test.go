package core

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killProcessesMatching kills processes whose command line names path and waits
// for them to exit: escaped leftovers reparent onto the test process and would
// show up in the next test's descendant scan.
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
