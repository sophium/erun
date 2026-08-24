package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestUsage(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"usage", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "usage/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		// --dry-run must trace the kubectl exec into the erun-devops container
		// and the reading script it would run, at the default 1s CPU sample
		// interval, without executing anything.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"usage", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "usage/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_custom_interval", func(t *testing.T) {
		// --interval changes the sample window baked into the traced script's
		// `sleep` line — the golden pins the flag actually reaching the plan.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"usage", "--interval", "2.5", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "usage/dry_run_custom_interval", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_missing_tenant", func(t *testing.T) {
		// No tenant configured at all: resolution fails before any kubectl call
		// is even planned, matching observe's equivalent scenario.
		setup := env.New(t)
		result := erun.Run(t, []string{"usage", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
		}
		golden.Equal(t, "usage/dry_run_missing_tenant", normalize.Apply(result.Combined))
	})

	// real_run_reports_cgroup_v2_usage is the scenario a metrics-server-based
	// design gets wrong: the reading has to come from inside the container's
	// own cgroup v2 accounting, not from a cluster add-on. The dry-run trace
	// alone cannot prove parsing correctness, so this stubs `kubectl exec` to
	// answer with a canned reading — the decision input dry-run cannot supply,
	// per erun-integration/AGENTS.md's "stubs as dry-run decision input" — and
	// asserts on the parsed JSON.
	t.Run("real_run_reports_cgroup_v2_usage", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExec(t, stubs, []string{
			"cgroup_type=cgroup2fs",
			"memory_current=413589504",
			"memory_max=2147483648",
			"memory_peak=1027301376",
			"memory_oom_kill=0",
			"cpu_max=100000 100000",
			"cpu_usage_before=581511501",
			"cpu_usage_after=581611501",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"cpu_periods=376556",
			"cpu_throttled_periods=425",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The watched mount is normalize.Apply's own <HOME> safety-net rule
		// (any /home/<user> path, not just the test's actual HOME), so the
		// snapshot cannot distinguish "/home/erun" from any other home
		// directory. Assert the literal, un-normalized value once here.
		if !strings.Contains(result.Combined, `"mount": "/home/erun"`) {
			t.Fatalf("expected the watched mount to be the literal /home/erun, got:\n%s", result.Combined)
		}
		// nr_periods/nr_throttled are the CPU-starvation signal
		// RuntimeUsageHistory derives its throttle ratio from; assert them here
		// since they carry no threshold of their own to warn on.
		if !strings.Contains(result.Combined, `"periods": 376556`) || !strings.Contains(result.Combined, `"throttledPeriods": 425`) {
			t.Fatalf("expected cpu.periods/throttledPeriods parsed from cpu.stat, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_reports_cgroup_v2_usage", normalize.Apply(result.Combined))
	})

	t.Run("real_run_cgroup_v1_reports_unavailable_not_an_error", func(t *testing.T) {
		// A cluster whose runtime image predates cgroup v2 (or any host where
		// /sys/fs/cgroup is not cgroup2fs) must still succeed: CPU and memory
		// report their own unavailability, matching the fail-soft posture
		// runtime_resources.go already takes for kubectl-top-less clusters.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExec(t, stubs, []string{
			"cgroup_type=tmpfs",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "usage/real_run_cgroup_v1_reports_unavailable_not_an_error", normalize.Apply(result.Combined))
	})

	// real_run_warns_near_the_memory_limit is the "an agent notices it is
	// heading for an OOM kill before it is killed" case #1233 exists for: a
	// reading nobody acts on is decoration, so a threshold crossing must show
	// up as a named warning field rather than requiring the caller to compute
	// the percentage itself.
	t.Run("real_run_warns_near_the_memory_limit", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExec(t, stubs, []string{
			"cgroup_type=cgroup2fs",
			"memory_current=1932735283", // ~90% of 2048Mi
			"memory_max=2147483648",
			"memory_peak=2100000000", // ~98% peak: also crosses the peak threshold
			"memory_oom_kill=1",
			"cpu_max=100000 100000",
			"cpu_usage_before=0",
			"cpu_usage_after=0",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"disk_workspace=overlay 198234112 99999999 99117056 95% /home/erun",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "usage/real_run_warns_near_the_memory_limit", normalize.Apply(result.Combined))
		if !strings.Contains(result.Combined, "\"warnings\"") {
			t.Fatalf("expected a warnings field in the JSON output, got:\n%s", result.Combined)
		}
	})
}

// stubUsageKubectlExec stubs `kubectl exec` to answer the usage reading
// script with fixed key=value lines, regardless of the script body in argv —
// the script itself only runs for real inside a live cgroup v2 container,
// which this harness does not have. Every other kubectl invocation exits 0
// silently.
func stubUsageKubectlExec(t testing.TB, stubsDir string, lines []string) {
	t.Helper()
	body := make([]string, 0, len(lines)+4)
	body = append(body, `case "$*" in`, `  *" exec "*)`)
	for _, line := range lines {
		body = append(body, `    printf '%s\n' '`+line+`'`)
	}
	body = append(body, `    ;;`, `esac`, `exit 0`)
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", strings.Join(body, "\n"))
}
