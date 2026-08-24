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
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	fields := make([]string, 0, 22)
	fields = append(fields, fmt.Sprintf("%d (%s) S", pid, comm))
	// Columns 4..13 (ppid through cmajflt) are unused padding here.
	for i := 0; i < 10; i++ {
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
