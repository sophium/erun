//go:build !windows

package eruncommon

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

// detachEnvironmentJobSupervisor puts the supervisor in its own session, so it
// has no controlling terminal to be hung up with and is not in the caller's
// process group. That is what makes a started job survive the call returning and
// the transport dropping, without the caller wrapping anything in setsid.
func detachEnvironmentJobSupervisor(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// detachEnvironmentJobChild gives the work its own process group and its own
// session, so cancelling a job reaches everything the work spawned and
// nothing else — in particular not the supervisor, which has to outlive the
// cancel to record its outcome. Setsid alone gives both: a session leader's
// sid and pgid are both its own pid by construction, and Setpgid is
// deliberately not also set alongside it -- setpgid(2) refuses outright on a
// process that is already a session leader (EPERM, "pid is a session
// leader"), so setting both flags together makes every job start fail. The
// pgid this produces is what environmentJobProcessGroupSurvivors already
// compared against; the sid is the wider boundary
// environmentJobSessionSurvivors now also compares against, owing nothing to
// whatever ambient session happens to be running the supervisor that spawned
// it. See that function for why the process group alone is not enough.
func detachEnvironmentJobChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// signalEnvironmentJobProcessGroup signals a recorded pid's whole process group.
// The two guards are the point: a caller can only ever name a pid a job record
// holds, and this refuses to signal the group the caller is itself in — which is
// how a pattern-matched kill once took out the sequence that issued it.
func signalEnvironmentJobProcessGroup(pid int, signal string) error {
	if pid <= 0 {
		return fmt.Errorf("job process id must be positive")
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refusing to signal this process (%d) as a job", pid)
	}
	group, err := syscall.Getpgid(pid)
	if err != nil {
		// The process is already gone; the record reconciles on the next read.
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

// environmentJobProcessGroupSurvivors reports whether any *live* process
// remains in the process group named by pgid, after its leader has already
// been waited on. detachEnvironmentJobChild always gives the work a fresh
// process group named after its own pid, so this is how a supervisor tells
// "the work exited clean" from "the work exited but left something it
// spawned still running".
//
// A raw `kill(-pgid, 0)` is not enough on its own: it also answers true for a
// zombie -- a process that already exited and is only waiting for its parent
// to reap it, left behind whenever an intermediate wrapper the job's command
// ran (not the supervisor) dies before it can wait() on its own child, e.g. a
// cancel's SIGTERM reaching the whole group at once. That shape is completed
// work nobody has reaped yet, not abandoned background work, so it must not
// read as a survivor. The platform's own process table tells the two apart,
// and `ps`'s STAT column after that; the signal probe is only the fallback for
// when neither can be consulted. That a zombie is not a survivor matters more
// now that the supervisor is a child subreaper: an orphan that dies as the
// work is being reaped is handed to the supervisor rather than to init, so it
// is this job's own process group that holds it while it waits to be reaped.
//
// The same SIGTERM that just ended the leader reaches every other group
// member at once, but the kernel does not process it atomically across
// processes -- a sibling can still read as alive for a few milliseconds
// while it finishes dying from that same signal, before it settles into the
// zombie state above. A reading taken the instant the leader is reaped can
// catch that transient window, so this gives a genuinely alive member a
// short settle budget to either exit or become a reapable zombie before it
// is trusted as a survivor.
func environmentJobProcessGroupSurvivors(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	deadline := time.Now().Add(environmentJobProcessGroupSurvivorSettleWindow)
	for {
		alive := environmentJobProcessGroupHasLiveMember(pgid)
		if !alive || !time.Now().Before(deadline) {
			return alive
		}
		time.Sleep(environmentJobProcessGroupSurvivorSettlePoll)
	}
}

const (
	environmentJobProcessGroupSurvivorSettleWindow = 300 * time.Millisecond
	environmentJobProcessGroupSurvivorSettlePoll   = 20 * time.Millisecond
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

// platformProcessGroupHasLiveMember answers the group question from this
// host's own process table, which is the only source that can tell a running
// member from one that has already exited (see groupHasLiveMember). It is the
// primary path for the same reason environmentJobSessionHasLiveMember's is:
// on a host with no `ps` to consult -- a distilled container image, or the
// integration suite's deliberately scrubbed PATH -- the signal probe below is
// all that is left, and that probe answers true for a zombie. The second
// return is false when the platform has no table to offer, so the caller falls
// back instead of trusting a default answer.
func platformProcessGroupHasLiveMember(pgid int) (bool, bool) {
	if procs, ok := environmentJobSessionProcessesFunc(); ok {
		return groupHasLiveMember(procs, pgid), true
	}
	return false, false
}

// psProcessGroupHasLiveMember answers whether pgid still has a non-zombie
// member, via `ps`'s STAT column. The second return is false when `ps` itself
// could not be run or its output could not be read, so the caller knows to
// fall back rather than trusting a default answer.
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

// environmentJobSessionSurvivors reports whether any *live* process remains
// in the session led by sid (the job's own childPID, which
// detachEnvironmentJobChild's Setsid makes a session leader in its own
// right, exactly as it already is a process-group leader), after the job's
// own tracked child has already been waited on.
//
// environmentJobProcessGroupSurvivors above cannot see everything this needs
// to catch: a process the work itself backgrounds into a *further* fresh
// process group of its own (an agent tool's Bash tool backgrounding a
// command precisely so it survives the turn that started it) escapes that
// narrower process-group scope. It does not escape the session, though,
// because nothing about ordinary job-control backgrounding calls setsid —
// and a session id is sticky even once the process is later reparented to
// init, which is what let a real leftover process observed in production
// still carry its session's original sid after losing its original parent.
func environmentJobSessionSurvivors(sid int) bool {
	if sid <= 0 {
		return false
	}
	deadline := time.Now().Add(environmentJobProcessGroupSurvivorSettleWindow)
	for {
		alive := environmentJobSessionHasLiveMember(sid)
		if !alive || !time.Now().Before(deadline) {
			return alive
		}
		time.Sleep(environmentJobProcessGroupSurvivorSettlePoll)
	}
}

// environmentJobSessionHasLiveMember asks this platform's own process table
// first — /proc on Linux, ps's pid and stat columns paired with getsid(2) on
// Darwin — and only falls back to ps's session column where the platform has
// no table to offer (see psSessionHasLiveMember for why that column cannot be
// the primary source).
func environmentJobSessionHasLiveMember(sid int) bool {
	if procs, ok := environmentJobSessionProcessesFunc(); ok {
		return sessionHasLiveMember(procs, sid)
	}
	return psSessionHasLiveMember(sid)
}

// psSessionHasLiveMember is psProcessGroupHasLiveMember's session-scoped
// twin: same zombie handling via the STAT column and the same exclusion of
// the session leader itself (pid == sid), which — like the group the pgid
// check is asked about — is the job's own tracked child, already reaped by
// the time this runs.
//
// It is the fallback for a platform with no session source of its own, not
// the primary check: the sess column it keys on is populated on Linux and
// empty on Darwin, where the kernel's user-visible kinfo_proc has nowhere to
// carry a session id, so `ps -o sess` prints 0 for every process. On Darwin
// this could only ever answer "no member" — the false success that
// platformSessionProcesses exists to answer instead.
func psSessionHasLiveMember(sid int) bool {
	out, err := exec.Command("ps", "-axo", "pid=,sess=,stat=").Output()
	if err != nil {
		return false
	}
	return parsePSSessionTable(out, sid)
}

// parsePSSessionTable finds a live, non-leader member of session sid in
// `ps -axo pid=,sess=,stat=` output. A row whose pid will not parse is
// skipped rather than compared, so it can never be mistaken for the leader.
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

// environmentJobExitSignal names the signal that ended the work, so a cancelled
// job reads as cancelled rather than as an exit code nobody chose.
func environmentJobExitSignal(state *os.ProcessState) string {
	if state == nil {
		return ""
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}
