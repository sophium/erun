//go:build darwin

package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// The columns Darwin can be trusted for: pid, ppid, pgid, and the state that
// tells a zombie apart from a process still running. The session is the column
// that cannot be read here, which is why the builder pairs this with getsid(2).
func TestParsePSProcessTableReadsPidsAndZombies(t *testing.T) {
	procs := parsePSProcessTable([]byte("" +
		"  101     1   101 Ss\n" +
		"  999   101   999 S\n" +
		" 4244   999   999 Z\n" +
		"  oops    1     1 S\n" +
		"  500  oops   500 S\n" +
		"  501     1  oops S\n" +
		"  502     1\n"))
	want := []sessionProcess{
		{pid: 101, parent: 1, group: 101},
		{pid: 999, parent: 101, group: 999},
		{pid: 4244, parent: 999, group: 999, zombie: true},
	}
	if len(procs) != len(want) {
		t.Fatalf("parsed %d processes, want %d: %+v", len(procs), len(want), procs)
	}
	for index, proc := range procs {
		if proc != want[index] {
			t.Fatalf("process %d parsed as %+v, want %+v", index, proc, want[index])
		}
	}
}

// The fixture above is only as good as its resemblance to what ps prints, so
// the real table must contain this test process with its real parent and group.
func TestPlatformSessionProcessesReadsThisProcessFromRealPS(t *testing.T) {
	procs, ok := platformSessionProcesses()
	if !ok {
		t.Fatal("platformSessionProcesses could not read the process table")
	}
	group, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	session, err := syscall.Getsid(os.Getpid())
	if err != nil {
		t.Fatalf("getsid: %v", err)
	}
	want := sessionProcess{pid: os.Getpid(), parent: os.Getppid(), group: group, session: session}
	for _, proc := range procs {
		if proc.pid == want.pid {
			if proc != want {
				t.Fatalf("this process read as %+v, want %+v", proc, want)
			}
			return
		}
	}
	t.Fatalf("this process (pid %d) is missing from %d parsed ps rows", want.pid, len(procs))
}

// Darwin is where the session check has to be native, so this exercises the
// real table — ps's pid and stat columns paired with getsid(2) — rather than
// substituting one: the session column ps reports here is 0 for every process,
// and a job that backgrounds its work into a fresh process group is invisible
// to any scan keyed on it.
func TestDarwinSessionScanFindsBackgroundedWorkInAFreshProcessGroup(t *testing.T) {
	backgroundLog := filepath.Join(t.TempDir(), "background.log")
	cmd := exec.Command("bash", "-c", fmt.Sprintf("set -m; sleep 5 </dev/null >%s 2>&1 & exit 0", backgroundLog))
	detachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { killProcessesMatching(backgroundLog) })
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	if !environmentJobSessionHasLiveMember(pid) {
		t.Fatalf("session %d still holds the backgrounded process, but the scan reported no live member", pid)
	}
}

// The other half of the same contract: a job that exits having started nothing
// must not read as having left a survivor behind, or every clean job would be
// reported as abandoned work.
func TestDarwinSessionScanSeesNoMemberInACleanJob(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	detachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	if environmentJobSessionHasLiveMember(pid) {
		t.Fatalf("session %d holds nothing but its reaped leader, but the scan reported a live member", pid)
	}
}
