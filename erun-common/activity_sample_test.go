package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The sampler reads a process filesystem, so a fixture tree is the only way to
// pin its verdict; a live /proc says something different on every machine.

// writeProcEntry lays down one /proc/<pid>/stat in the fixture shape, including
// a comm with a space and a paren so the parser is exercised against the field
// that actually breaks naive splitting.
func writeProcEntry(t *testing.T, root string, pid int, comm string, cpuTicks, startTime int64) {
	t.Helper()
	writeProcEntryWithTTY(t, root, pid, comm, cpuTicks, startTime, 0)
}

// writeProcEntryWithTTY is writeProcEntry with an explicit tty_nr (column 7),
// so a scenario can lay down a process that holds - or does not hold - an
// allocated pseudo-terminal.
func writeProcEntryWithTTY(t *testing.T, root string, pid int, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	writeProcEntryFull(t, root, pid, 0, comm, cpuTicks, startTime, ttyNr)
}

// writeProcEntryFull is writeProcEntryWithTTY with an explicit ppid (column
// 4), so a scenario can model the real shape of an SSH session: the
// per-session "sshd: user@ptsN" process stays tty-less itself while the
// command it forks becomes the pty's session leader.
func writeProcEntryFull(t *testing.T, root string, pid int, ppid int64, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	fields := make([]string, 0, 22)
	fields = append(fields, fmt.Sprintf("%d (%s) S", pid, comm))
	// Column 4 (ppid).
	fields = append(fields, fmt.Sprintf("%d", ppid))
	// Column 5 (pgrp) is unused padding.
	fields = append(fields, "0")
	// Column 6 (session) is unused padding.
	fields = append(fields, "0")
	// Column 7 (tty_nr): 0 means no controlling terminal.
	fields = append(fields, fmt.Sprintf("%d", ttyNr))
	// Columns 8..13 (tpgid through cmajflt) are unused padding here.
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	// utime + stime, split so the parser has to sum them.
	fields = append(fields, fmt.Sprintf("%d", cpuTicks/2), fmt.Sprintf("%d", cpuTicks-cpuTicks/2))
	// Columns 16..21.
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, fmt.Sprintf("%d", startTime))
	body := ""
	for i, field := range fields {
		if i > 0 {
			body += " "
		}
		body += field
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(body+" 0 0\n"), 0o644); err != nil {
		t.Fatalf("write stat: %v", err)
	}
}

func TestResidentActivityNeedsCPUToAdvance(t *testing.T) {
	root := t.TempDir()
	writeProcEntry(t, root, 101, "java", 500, 900)
	// An agent parked at a prompt: resident for hours, burning nothing. Counting
	// residency alone would pin the environment awake forever.
	writeProcEntry(t, root, 102, "claude-real", 200, 901)
	// The sampler's own process must never be its own evidence.
	writeProcEntry(t, root, 103, "erun", 10, 902)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	first, err := ScanResidentActivity(root, 103, ResidentActivitySample{}, now)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.Busy {
		t.Fatalf("the first sample has nothing to compare against and must report idle, got %+v", first)
	}
	if _, ok := first.Sample.CPU["103-902"]; ok {
		t.Error("the sampler must exclude its own process from the accounting")
	}

	// Second tick: the build advanced by 1500 ticks (50% of one core over the
	// 30s interval) — well clear of the rate floor — while the parked agent
	// did not advance at all.
	writeProcEntry(t, root, 101, "java", 2000, 900)
	second, err := ScanResidentActivity(root, 103, first.Sample, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if !second.Busy {
		t.Fatal("a build burning CPU must read as working")
	}
	if !reflect.DeepEqual(second.Processes, []string{"java"}) {
		t.Errorf("expected only the advancing process named, got %v", second.Processes)
	}

	// Third tick: nothing moved.
	third, err := ScanResidentActivity(root, 103, second.Sample, now.Add(60*time.Second))
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if third.Busy {
		t.Errorf("a quiet tick must read as idle, got %+v", third)
	}
}

// TestResidentActivityIgnoresNoiseBelowTheCPURateFloor pins the fix: a parked
// session's CPU delta is never exactly zero — scheduler and terminal-repaint
// noise advances it by a tick or two every sample — so a bare "advanced at
// all" test clears on every single tick and pins the environment busy
// forever. The numbers here are the ones measured against a real parked
// `claude-real` on frs/local: 21 ticks (210ms) over a 30s sample, ~0.7% of one
// core.
func TestResidentActivityIgnoresNoiseBelowTheCPURateFloor(t *testing.T) {
	root := t.TempDir()
	writeProcEntry(t, root, 201, "claude-real", 21529+3884, 900)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	first, err := ScanResidentActivity(root, 1, ResidentActivitySample{}, now)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	// The parked session's ticks advance by 21 over the 30s sample — noise,
	// not work.
	writeProcEntry(t, root, 201, "claude-real", 21529+3884+21, 900)
	second, err := ScanResidentActivity(root, 1, first.Sample, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.Busy {
		t.Errorf("a parked session's tick-level noise must not read as work, got %+v", second)
	}

	// A zero elapsed interval (two samples stamped at the same instant) must
	// not divide by zero or read busy, even with the same delta that would
	// otherwise clear the floor over a real interval.
	writeProcEntry(t, root, 201, "claude-real", 21529+3884+2100, 900)
	sampledAt := now.Add(30 * time.Second)
	third, err := ScanResidentActivity(root, 1, ResidentActivitySample{SampledAt: sampledAt, CPU: second.Sample.CPU}, sampledAt)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if third.Busy {
		t.Errorf("an unmeasurable (zero) interval must not read as work, got %+v", third)
	}
}

func TestResidentActivityCountsANewlyStartedProcess(t *testing.T) {
	// A build that just started has burned nothing yet, so a pure delta rule
	// would miss its whole first tick.
	root := t.TempDir()
	writeProcEntry(t, root, 201, "node", 40, 700)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	first, err := ScanResidentActivity(root, 1, ResidentActivitySample{}, now)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	writeProcEntry(t, root, 202, "gradle", 0, 800)
	second, err := ScanResidentActivity(root, 1, first.Sample, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if !second.Busy || !reflect.DeepEqual(second.Processes, []string{"gradle"}) {
		t.Errorf("expected the newly started process to read as working, got %+v", second)
	}
}

func TestResidentActivityIgnoresProcessesNobodyWouldCallWork(t *testing.T) {
	root := t.TempDir()
	writeProcEntry(t, root, 301, "sshd", 100, 500)
	writeProcEntry(t, root, 302, "emcp", 100, 501)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	first, err := ScanResidentActivity(root, 1, ResidentActivitySample{}, now)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(first.Sample.CPU) != 0 {
		t.Fatalf("expected no accounting for pod furniture, got %+v", first.Sample.CPU)
	}
	writeProcEntry(t, root, 301, "sshd", 999, 500)
	second, err := ScanResidentActivity(root, 1, first.Sample, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.Busy {
		t.Errorf("the pod's own long-lived services must not read as work, got %+v", second)
	}
}

// TestScanInteractiveSSHSession pins the discriminator that separates an
// operator at a real shell from erun's own port-forward and sync traffic.
// Privilege-separated sshd never puts the pty on its own process: the
// per-session "sshd: user@ptsN" process (measured tty_nr=0, matching a real
// runtime pod) forks the command or shell that actually becomes the pty's
// session leader, so the check has to follow that parent/child edge rather
// than reading one process's own fields.
func TestScanInteractiveSSHSession(t *testing.T) {
	root := t.TempDir()
	if found, err := ScanInteractiveSSHSession(root); err != nil || found {
		t.Fatalf("expected no session with an empty /proc, got found=%v err=%v", found, err)
	}

	// The listener itself: daemonized, no controlling terminal, no children.
	writeProcEntryFull(t, root, 1, 0, "sshd", 10, 100, 0)
	if found, err := ScanInteractiveSSHSession(root); err != nil || found {
		t.Fatalf("expected the sshd listener alone to read as no session, got found=%v err=%v", found, err)
	}

	// A workspace-sync/sftp channel: the per-session sshd child (tty-less,
	// like the real process) forks a transfer helper that never requested a
	// pty either - "notty" end to end.
	writeProcEntryFull(t, root, 2, 1, "sshd", 10, 101, 0)
	writeProcEntryFull(t, root, 3, 2, "sftp-server", 10, 102, 0)
	if found, err := ScanInteractiveSSHSession(root); err != nil || found {
		t.Fatalf("expected a notty session to read as no session, got found=%v err=%v", found, err)
	}

	// A real interactive session: the per-session sshd child forks a shell
	// that holds the allocated pty.
	writeProcEntryFull(t, root, 4, 1, "sshd", 10, 103, 0)
	writeProcEntryFull(t, root, 5, 4, "bash", 10, 104, 34816)
	if found, err := ScanInteractiveSSHSession(root); err != nil || !found {
		t.Fatalf("expected a pty-holding session to read as active, got found=%v err=%v", found, err)
	}
}

// TestScanInteractiveSSHSessionIgnoresNonSSHDProcessesWithATTY guards against
// a broader match than intended: a shell or editor with a real tty must not
// itself count unless its parent is actually an sshd process.
func TestScanInteractiveSSHSessionIgnoresNonSSHDProcessesWithATTY(t *testing.T) {
	root := t.TempDir()
	writeProcEntryFull(t, root, 10, 1, "tmux", 10, 100, 34816)
	writeProcEntryFull(t, root, 11, 10, "bash", 10, 101, 34816)
	if found, err := ScanInteractiveSSHSession(root); err != nil || found {
		t.Fatalf("expected a tty-holding process with no sshd parent to read as no session, got found=%v err=%v", found, err)
	}
}
