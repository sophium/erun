package sampling

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// comm carries a space and a paren to exercise the parser's hardest field.
func writeProcEntry(t *testing.T, root string, pid int, comm string, cpuTicks, startTime int64) {
	t.Helper()
	writeProcEntryWithTTY(t, root, pid, comm, cpuTicks, startTime, 0)
}

func writeProcEntryWithTTY(t *testing.T, root string, pid int, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	writeProcEntryFull(t, root, pid, 0, comm, cpuTicks, startTime, ttyNr)
}

func writeProcEntryFull(t *testing.T, root string, pid int, ppid int64, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	fields := make([]string, 0, 22)
	fields = append(fields, fmt.Sprintf("%d (%s) S", pid, comm))
	fields = append(fields, fmt.Sprintf("%d", ppid))
	fields = append(fields, "0")
	fields = append(fields, "0")
	// Column 7 (tty_nr): 0 means no controlling terminal.
	fields = append(fields, fmt.Sprintf("%d", ttyNr))
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	// utime + stime, split so the parser has to sum them.
	fields = append(fields, fmt.Sprintf("%d", cpuTicks/2), fmt.Sprintf("%d", cpuTicks-cpuTicks/2))
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

// Noise measured on a real parked agent: 21 ticks over a 30s sample, ~0.7% of a core.
func TestResidentActivityIgnoresNoiseBelowTheCPURateFloor(t *testing.T) {
	root := t.TempDir()
	writeProcEntry(t, root, 201, "claude-real", 21529+3884, 900)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	first, err := ScanResidentActivity(root, 1, model.ResidentActivitySample{}, now)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	writeProcEntry(t, root, 201, "claude-real", 21529+3884+21, 900)
	second, err := ScanResidentActivity(root, 1, first.Sample, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.Busy {
		t.Errorf("a parked session's tick-level noise must not read as work, got %+v", second)
	}

	// A zero interval must not divide by zero or read busy.
	writeProcEntry(t, root, 201, "claude-real", 21529+3884+2100, 900)
	sampledAt := now.Add(30 * time.Second)
	third, err := ScanResidentActivity(root, 1, model.ResidentActivitySample{SampledAt: sampledAt, CPU: second.Sample.CPU}, sampledAt)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if third.Busy {
		t.Errorf("an unmeasurable (zero) interval must not read as work, got %+v", third)
	}
}

func TestResidentActivityCountsANewlyStartedProcess(t *testing.T) {
	// A just-started build has burned nothing yet but is work.
	root := t.TempDir()
	writeProcEntry(t, root, 201, "node", 40, 700)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	first, err := ScanResidentActivity(root, 1, model.ResidentActivitySample{}, now)
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
	first, err := ScanResidentActivity(root, 1, model.ResidentActivitySample{}, now)
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

func TestScanInteractiveSSHSessionIgnoresNonSSHDProcessesWithATTY(t *testing.T) {
	root := t.TempDir()
	writeProcEntryFull(t, root, 10, 1, "tmux", 10, 100, 34816)
	writeProcEntryFull(t, root, 11, 10, "bash", 10, 101, 34816)
	if found, err := ScanInteractiveSSHSession(root); err != nil || found {
		t.Fatalf("expected a tty-holding process with no sshd parent to read as no session, got found=%v err=%v", found, err)
	}
}
