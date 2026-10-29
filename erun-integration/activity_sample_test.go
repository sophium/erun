package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func writeSampleProcess(t *testing.T, root string, pid int, comm string, cpuTicks, startTime int64) {
	t.Helper()
	writeSampleProcessWithTTY(t, root, pid, comm, cpuTicks, startTime, 0)
}

func writeSampleProcessWithTTY(t *testing.T, root string, pid int, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	writeSampleProcessFull(t, root, pid, 0, comm, cpuTicks, startTime, ttyNr)
}

// writeSampleProcessFull adds ppid, to model sshd's tty-less session process
// forking a pty session leader.
func writeSampleProcessFull(t *testing.T, root string, pid int, ppid int64, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	fields := []string{fmt.Sprintf("%d (%s) S", pid, comm)}
	fields = append(fields, fmt.Sprintf("%d", ppid))
	fields = append(fields, "0", "0")
	// tty_nr 0 means no controlling terminal.
	fields = append(fields, fmt.Sprintf("%d", ttyNr))
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, fmt.Sprintf("%d", cpuTicks), "0")
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, fmt.Sprintf("%d", startTime), "0", "0")
	body := strings.Join(fields, " ") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s/stat: %v", dir, err)
	}
}

func writeCgroupFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", root, name, err)
		}
	}
}

// cgroupV2Fixture is the shape measured in a live runtime container.
func cgroupV2Fixture(usageUsec, periods, throttled, peakBytes, oomKills int64) map[string]string {
	return map[string]string{
		"cpu.max":        "1200000 100000\n",
		"cpu.stat":       fmt.Sprintf("usage_usec %d\nnr_periods %d\nnr_throttled %d\nthrottled_usec 0\n", usageUsec, periods, throttled),
		"memory.max":     "24696061952\n",
		"memory.current": "4773695488\n",
		"memory.peak":    fmt.Sprintf("%d\n", peakBytes),
		"memory.events":  fmt.Sprintf("low 0\nhigh 0\nmax 0\noom 0\noom_kill %d\n", oomKills),
	}
}

func readUsageHistory(t *testing.T, cacheHome, tenant, environment string) map[string]any {
	t.Helper()
	path := filepath.Join(cacheHome, "erun", "activity", tenant, environment, "usage-history.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var history map[string]any
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return history
}

func testActivitySampleRecordsActivityOnlyWhenWorkAdvances(t *testing.T) {
	// Residency alone is not work; the CPU delta is.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	writeSampleProcess(t, procRoot, 101, "java", 500, 900)
	// An absent cgroup root keeps host usage out of this scenario.
	sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}

	first := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	writeSampleProcess(t, procRoot, 101, "java", 900, 900)
	working := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	quiet := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	golden.Equal(t, "activity/sample_records_activity_only_when_work_advances", normalize.Apply(
		first.Combined+working.Combined+quiet.Combined))

	if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "process.json")); err != nil {
		t.Errorf("expected the working sample to record process activity: %v", err)
	}
}

func testActivitySampleRecordsSSHActivityForAPtyHoldingSession(t *testing.T) {
	// A tty-less sshd child forking a pty-holding shell is SSH activity.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	writeSampleProcessFull(t, procRoot, 401, 1, "sshd", 10, 900, 0)
	writeSampleProcessFull(t, procRoot, 403, 401, "bash", 10, 901, 34816)
	result := erun.Run(t, []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	body, err := os.ReadFile(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "ssh.json"))
	if err != nil {
		t.Fatalf("expected the pty-holding session to record ssh activity: %v", err)
	}
	if !strings.Contains(string(body), `"lastActivity"`) {
		t.Errorf("expected lastActivity recorded for a real session, got:\n%s", body)
	}
}

func testActivitySampleIgnoresANottySSHChild(t *testing.T) {
	// Without a pty (port-forward, sync channel) it must not be.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	writeSampleProcessFull(t, procRoot, 402, 1, "sshd", 10, 900, 0)
	writeSampleProcessFull(t, procRoot, 404, 402, "sftp-server", 10, 901, 0)
	result := erun.Run(t, []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "ssh.json")); !os.IsNotExist(err) {
		t.Errorf("expected no ssh activity recorded for a notty sshd child, stat err: %v", err)
	}
}

func testActivitySampleRetainsRuntimeUsageFromCgroupCounters(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	cgroupRoot := filepath.Join(setup.Cwd, "cgroup")
	writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(27551234478, 376556, 0, 12742377472, 0))
	sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", cgroupRoot}

	first := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(28551234478, 386556, 0, 12742377472, 0))
	second := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	golden.Equal(t, "activity/sample_retains_runtime_usage_from_cgroup_counters", normalize.Apply(first.Combined+second.Combined))

	history := readUsageHistory(t, setup.CacheHome, "team", "dev")
	if got := history["observedPeakMemoryBytes"]; got != float64(12742377472) {
		t.Errorf("observedPeakMemoryBytes = %v, want the cgroup high-water 12742377472", got)
	}
	if got := history["observedPeriods"]; got != float64(386556) {
		t.Errorf("observedPeriods = %v, want the first lifetime's total plus the delta", got)
	}
	if samples, ok := history["samples"].([]any); !ok || len(samples) != 2 {
		t.Errorf("samples = %v, want both ticks retained", history["samples"])
	}
}

func testActivitySampleRetainsThePeakAcrossAContainerRestart(t *testing.T) {
	// memory.peak resets on restart, so the pre-restart high-water must be retained.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	cgroupRoot := filepath.Join(setup.Cwd, "cgroup")
	sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", cgroupRoot}

	writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(27551234478, 376556, 12, 22000000000, 1))
	erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(4000, 40, 0, 900000000, 0))
	erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})

	history := readUsageHistory(t, setup.CacheHome, "team", "dev")
	if got := history["restarts"]; got != float64(1) {
		t.Errorf("restarts = %v, want 1 inferred from the counters going backwards", got)
	}
	if got := history["observedPeakMemoryBytes"]; got != float64(22000000000) {
		t.Errorf("observedPeakMemoryBytes = %v, want the pre-restart high-water 22000000000", got)
	}
	if got := history["observedOomKills"]; got != float64(1) {
		t.Errorf("observedOomKills = %v, want the pre-restart kill retained", got)
	}
	if got := history["observedThrottledPeriods"]; got != float64(12) {
		t.Errorf("observedThrottledPeriods = %v, want the pre-restart throttling retained", got)
	}
}

func testActivitySampleRecordsNoUsageWhenTheHostSuppliesNoCounters(t *testing.T) {
	// No cgroup v2: nothing is written.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	result := erun.Run(t, []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "absent")},
		erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("an absent cgroup must not fail the monitor tick: exit %d\n%s", result.ExitCode, result.Combined)
	}
	path := filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "usage-history.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no usage history without counters, stat err = %v", err)
	}
}

func testActivitySampleRequiresTarget(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "sample"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/sample_requires_target", normalize.Apply(result.Combined))
}
