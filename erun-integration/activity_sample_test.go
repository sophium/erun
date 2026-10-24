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

// writeSampleProcess lays down one /proc/<pid>/stat entry so the sampler's
// verdict is driven by the fixture rather than by whatever the host happens to
// be running.
func writeSampleProcess(t *testing.T, root string, pid int, comm string, cpuTicks, startTime int64) {
	t.Helper()
	writeSampleProcessWithTTY(t, root, pid, comm, cpuTicks, startTime, 0)
}

// writeSampleProcessWithTTY is writeSampleProcess with an explicit tty_nr
// (column 7) and no parent, for a lone process with - or without - an
// allocated pseudo-terminal.
func writeSampleProcessWithTTY(t *testing.T, root string, pid int, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	writeSampleProcessFull(t, root, pid, 0, comm, cpuTicks, startTime, ttyNr)
}

// writeSampleProcessFull is writeSampleProcessWithTTY with an explicit ppid
// (column 4), so a scenario can model the real shape of an SSH session: the
// per-session "sshd: user@ptsN" process stays tty-less itself while the
// command it forks becomes the pty's session leader.
func writeSampleProcessFull(t *testing.T, root string, pid int, ppid int64, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	fields := []string{fmt.Sprintf("%d (%s) S", pid, comm)}
	// Column 4 (ppid).
	fields = append(fields, fmt.Sprintf("%d", ppid))
	// Columns 5..6 (pgrp, session) are unused padding.
	fields = append(fields, "0", "0")
	// Column 7 (tty_nr): 0 means no controlling terminal.
	fields = append(fields, fmt.Sprintf("%d", ttyNr))
	// Columns 8..13 (tpgid through cmajflt) are unused padding.
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

// writeCgroupFixture lays down a cgroup v2 tree so the usage reader's verdict
// comes from the fixture. The real /sys/fs/cgroup says something different on
// every machine, and on a cgroup v1 host it says nothing at all.
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

// cgroupV2Fixture is the shape measured in a live runtime container: a 12-core
// quota, a 23552Mi limit, and a high-water mark at roughly half of it.
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

// readUsageHistory reads the retained usage store. It is a side effect outside
// the captured streams, so a golden cannot assert it.
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
	// The uninstrumented-work fallback. Driven against a fixture process tree
	// because a real /proc says something different on every machine, and the
	// point of the CPU-delta rule is that residency alone is not work.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	writeSampleProcess(t, procRoot, 101, "java", 500, 900)
	// The same tick also retains a usage reading. Point it at an absent
	// cgroup root so this scenario stays about the process sampler and reads
	// nothing ambient from the host that ran it.
	sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}

	first := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	writeSampleProcess(t, procRoot, 101, "java", 900, 900)
	working := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	quiet := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	golden.Equal(t, "activity/sample_records_activity_only_when_work_advances", normalize.Apply(
		first.Combined+working.Combined+quiet.Combined))

	// The recorded activity is the side effect the idle decision reads, and it
	// lives outside the captured streams.
	if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "process.json")); err != nil {
		t.Errorf("expected the working sample to record process activity: %v", err)
	}
}

func testActivitySampleRecordsSSHActivityForAPtyHoldingSession(t *testing.T) {
	// A real interactive session is an sshd child ("sshd:
	// user@ptsN", itself tty-less) that forks a shell holding the allocated
	// pseudo-terminal. That must read as SSH activity even though nothing
	// crossed the host-side forward that this fixture cannot simulate.
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
	// An sshd child whose forked command never allocated a pty -
	// the shape a port-forward re-establishment or a background sync
	// channel takes - must never read as SSH activity, or the phantom-session
	// bug this fixes comes back.
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
	// The collection half of the sizing recommendation. The monitor's tick
	// already runs this command, so the history accumulates with no second
	// scheduled job.
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
	// memory.peak resets when the container restarts, which is exactly why
	// this is a store and not a live read: without retention the pre-restart
	// high-water is simply gone, and an environment gets sized from whatever
	// it happens to have done since.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	procRoot := filepath.Join(setup.Cwd, "proc")
	cgroupRoot := filepath.Join(setup.Cwd, "cgroup")
	sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", cgroupRoot}

	writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(27551234478, 376556, 12, 22000000000, 1))
	erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	// Every cumulative counter starts over, and the fresh peak is far below
	// the one already observed.
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
	// cgroup v1, or a laptop. A history of empty samples could only ever
	// support "no evidence", so nothing is written at all.
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
