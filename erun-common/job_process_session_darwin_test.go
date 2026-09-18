//go:build darwin

package eruncommon

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// The two columns Darwin can be trusted for: pids, and the state that tells a
// zombie apart from a process still running. The session is the column that
// cannot be read here, which is why the builder pairs this with getsid(2).
func TestParsePSProcessTableReadsPidsAndZombies(t *testing.T) {
	procs := parsePSProcessTable([]byte("  101 Ss\n  999 S\n 4244 Z\n   oops S\n"))
	want := []sessionProcess{{pid: 101}, {pid: 999}, {pid: 4244, zombie: true}}
	if len(procs) != len(want) {
		t.Fatalf("parsed %d processes, want %d: %+v", len(procs), len(want), procs)
	}
	for index, proc := range procs {
		if proc != want[index] {
			t.Fatalf("process %d parsed as %+v, want %+v", index, proc, want[index])
		}
	}
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
