//go:build !windows

package core

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DetachEnvironmentJobSupervisor gives the supervisor its own session so it
// survives the caller and the transport dropping.
func DetachEnvironmentJobSupervisor(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// DetachEnvironmentJobChild gives the work its own session and process group so
// a cancel reaches all of it but not the supervisor. Setpgid must not be set too:
// setpgid(2) fails with EPERM on a session leader.
func DetachEnvironmentJobChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// SignalEnvironmentJobProcessGroup signals a recorded pid's process group, but
// never the caller's own group.
func SignalEnvironmentJobProcessGroup(pid int, signal string) error {
	if pid <= 0 {
		return fmt.Errorf("job process id must be positive")
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refusing to signal this process (%d) as a job", pid)
	}
	group, alive := processGroupOf(pid)
	if !alive {
		return nil
	}
	if own, err := syscall.Getpgid(os.Getpid()); err == nil && own == group {
		return fmt.Errorf("refusing to signal process group %d: it is this process's own group", group)
	}
	number, err := environmentJobSignalNumber(signal)
	if err != nil {
		return err
	}
	if err := syscall.Kill(-group, number); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("signal job process group %d: %w", group, err)
	}
	return nil
}

// EnvironmentJobProcessGroupSurvivors reports a live process left in pgid after
// its leader was reaped. Zombies are completed work, so the process table or ps
// STAT is preferred over kill(-pgid, 0). A short settle budget lets members still
// dying from the same SIGTERM finish.
func EnvironmentJobProcessGroupSurvivors(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	deadline := time.Now().Add(EnvironmentJobProcessGroupSurvivorSettleWindow)
	for {
		alive := environmentJobProcessGroupHasLiveMember(pgid)
		if !alive || !time.Now().Before(deadline) {
			return alive
		}
		time.Sleep(EnvironmentJobProcessGroupSurvivorSettlePoll)
	}
}

const (
	EnvironmentJobProcessGroupSurvivorSettleWindow = 300 * time.Millisecond
	EnvironmentJobProcessGroupSurvivorSettlePoll   = 20 * time.Millisecond
)

func environmentJobProcessGroupHasLiveMember(pgid int) bool {
	if alive, ok := platformProcessGroupHasLiveMember(pgid); ok {
		return alive
	}
	if alive, ok := psProcessGroupHasLiveMember(pgid); ok {
		return alive
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// platformProcessGroupHasLiveMember answers from the process table, which is the
// primary source because hosts may lack ps. ok is false when there is no table.
func platformProcessGroupHasLiveMember(pgid int) (bool, bool) {
	if procs, ok := EnvironmentJobSessionProcessesFunc(); ok {
		return groupHasLiveMember(procs, pgid), true
	}
	return false, false
}

// psProcessGroupHasLiveMember checks pgid via ps STAT; ok is false when ps fails.
func psProcessGroupHasLiveMember(pgid int) (bool, bool) {
	out, err := exec.Command("ps", "-axo", "pid=,pgid=,stat=").Output()
	if err != nil {
		return false, false
	}
	target := strconv.Itoa(pgid)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != target {
			continue
		}
		if !psStatIsZombie(fields[2]) {
			return true, true
		}
	}
	return false, true
}

// EnvironmentJobSessionSurvivors reports a live process left in session sid. It
// catches work backgrounded into a fresh process group, which job control does
// without setsid; the sid survives reparenting to init.
func EnvironmentJobSessionSurvivors(sid int) bool {
	if sid <= 0 {
		return false
	}
	deadline := time.Now().Add(EnvironmentJobProcessGroupSurvivorSettleWindow)
	for {
		alive := environmentJobSessionHasLiveMember(sid)
		if !alive || !time.Now().Before(deadline) {
			return alive
		}
		time.Sleep(EnvironmentJobProcessGroupSurvivorSettlePoll)
	}
}

// environmentJobSessionHasLiveMember prefers the platform table and falls back to ps.
func environmentJobSessionHasLiveMember(sid int) bool {
	if procs, ok := EnvironmentJobSessionProcessesFunc(); ok {
		return sessionHasLiveMember(procs, sid)
	}
	return psSessionHasLiveMember(sid)
}

// psSessionHasLiveMember is the ps-based fallback, excluding the leader. Darwin's
// sess column is always 0, so it cannot be the primary source there.
func psSessionHasLiveMember(sid int) bool {
	out, err := exec.Command("ps", "-axo", "pid=,sess=,stat=").Output()
	if err != nil {
		return false
	}
	return parsePSSessionTable(out, sid)
}

// parsePSSessionTable finds a live non-leader member of sid; unparseable pids are skipped.
func parsePSSessionTable(out []byte, sid int) bool {
	target := strconv.Itoa(sid)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != target {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == sid {
			continue
		}
		if !psStatIsZombie(fields[2]) {
			return true
		}
	}
	return false
}

func environmentJobSignalNumber(signal string) (syscall.Signal, error) {
	switch signal {
	case "TERM":
		return syscall.SIGTERM, nil
	case "INT":
		return syscall.SIGINT, nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "KILL":
		return syscall.SIGKILL, nil
	default:
		return 0, fmt.Errorf("unsupported signal %q", signal)
	}
}

// EnvironmentJobExitSignal names the signal that ended the work, so a cancel reads as one.
func EnvironmentJobExitSignal(state *os.ProcessState) string {
	if state == nil {
		return ""
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}

func processGroupOf(pid int) (int, bool) {
	group, err := syscall.Getpgid(pid)
	return group, err == nil
}
