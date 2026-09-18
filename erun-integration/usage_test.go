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
	t.Parallel()
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

	// real_run_runtime_env_omits_builds_caveat is the negative case: a
	// runtime-type env has no erun-dind sidecar (every image build for it
	// happens elsewhere, never in this pod), so the reading is the whole
	// story and must not carry the "excludes builds" caveat that a
	// build-capable env's own reading (the scenario above) does carry. Locks
	// EnvironmentType.UsesDindSidecar's runtime=false branch end-to-end
	// through the real command, not just the unit-level predicate.
	t.Run("real_run_runtime_env_omits_builds_caveat", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
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
		if strings.Contains(result.Combined, "excludesBuilds") {
			t.Fatalf("a runtime-type env has no dind sidecar and must not report excludesBuilds, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_runtime_env_omits_builds_caveat", normalize.Apply(result.Combined))
	})

	// real_run_local_agent_env_states_the_builds_caveat is the positive
	// sibling: a build-capable env (local-agent/remote-agent) carries the
	// erun-dind sidecar every image build actually runs in, a separate cgroup
	// this reading cannot see, so the JSON output must say so rather than let
	// a busy build read as an idle environment.
	t.Run("real_run_local_agent_env_states_the_builds_caveat", func(t *testing.T) {
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
		result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "erun-dind sidecar") {
			t.Fatalf("a build-capable env must state the builds-excluded caveat, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_local_agent_env_states_the_builds_caveat", normalize.Apply(result.Combined))
	})

	// real_run_build_saturated_env_reports_build_cpu is the defect this fix
	// exists for: an environment pinned at its 4-core build cap by a running
	// build used to report ~0.1% CPU on every surface, because the reading
	// samples the runtime container's cgroup and the build runs in a sibling
	// one under the erun-dind sidecar. The operator reading this command must
	// see the build's own number -- saturated, and throttled -- rather than a
	// near-idle environment. Both cgroups are stubbed: the runtime container
	// genuinely is idle (its CPU is the modest figure the runtime line
	// reports), and only the build cgroup read, which needs the pod's own
	// identity to be reachable at all, shows the work.
	t.Run("real_run_build_saturated_env_reports_build_cpu", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExecWithBuildCgroup(t, stubs, []string{
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
		}, []string{
			// 400000/100000 is the 4-core cap dind-entrypoint.sh mirrors from
			// the sidecar's own cpu.max; 4.0 CPU-seconds over the script's own
			// 1s window is exactly that cap, throttled in every period.
			"cgroup_type=cgroup2fs",
			"cpu_max=400000 100000",
			"cpu_usage_before=1000000",
			"cpu_usage_after=5000000",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"cpu_periods=200",
			"cpu_throttled_periods=200",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "Build CPU: 100.0% of a 4.00-core quota") {
			t.Fatalf("a build pinned at its cap must report its own CPU, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_build_saturated_env_reports_build_cpu", normalize.Apply(result.Combined))
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

	// real_run_unreadable_peak_renders_unavailable_not_zero: an unreadable
	// memory.peak (an older cgroup v2 kernel, or cgroup v1) must render as
	// unavailable, not as a measured "peak 0B" -- the reassuring wrong answer
	// for the least known state.
	t.Run("real_run_unreadable_peak_renders_unavailable_not_zero", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExec(t, stubs, []string{
			"cgroup_type=cgroup2fs",
			"memory_current=413589504",
			"memory_max=2147483648",
			"memory_peak=",
			"memory_oom_kill=0",
			"cpu_max=100000 100000",
			"cpu_usage_before=0",
			"cpu_usage_after=0",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "peak 0B") {
			t.Fatalf("an unreadable peak must never render as a measured 0B, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_unreadable_peak_renders_unavailable_not_zero", normalize.Apply(result.Combined))
	})

	// real_run_unreadable_peak_json_omits_peak_fields is the JSON-mode sibling
	// of the scenario above: the wire shape must let a consumer tell "peak not
	// readable here" from "peak is zero" (peakObserved's presence), and the
	// close-to-OOM warning must not compute a percentage from a reading it
	// never took, even though current usage alone is nowhere near either
	// threshold in this fixture.
	t.Run("real_run_unreadable_peak_json_omits_peak_fields", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExec(t, stubs, []string{
			"cgroup_type=cgroup2fs",
			"memory_current=413589504",
			"memory_max=2147483648",
			"memory_peak=",
			"memory_oom_kill=0",
			"cpu_max=100000 100000",
			"cpu_usage_before=0",
			"cpu_usage_after=0",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "peakBytes") || strings.Contains(result.Combined, "peakObserved") {
			t.Fatalf("an unreadable peak must omit both peakBytes and peakObserved, got:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "memory.peak reached") {
			t.Fatalf("an unreadable peak must not warn from a percentage it never computed, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_unreadable_peak_json_omits_peak_fields", normalize.Apply(result.Combined))
	})

	// real_run_genuine_zero_peak_renders_unchanged is the regression guard for
	// the sibling of the two scenarios above: a memory.peak that was actually
	// read as zero must keep rendering exactly as before this fix, since a
	// real zero is not the unknown state the two scenarios above cover.
	t.Run("real_run_genuine_zero_peak_renders_unchanged", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExec(t, stubs, []string{
			"cgroup_type=cgroup2fs",
			"memory_current=0",
			"memory_max=2147483648",
			"memory_peak=0",
			"memory_oom_kill=0",
			"cpu_max=100000 100000",
			"cpu_usage_before=0",
			"cpu_usage_after=0",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "peak 0B") {
			t.Fatalf("a genuinely-zero, observed peak must still render as peak 0B, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_genuine_zero_peak_renders_unchanged", normalize.Apply(result.Combined))
	})
}

// stubUsageKubectlExec stubs `kubectl exec` to answer the usage reading
// script with fixed key=value lines, regardless of the script body in argv —
// the script itself only runs for real inside a live cgroup v2 container,
// which this harness does not have. Every other kubectl invocation exits 0
// silently.
//
// The build-cgroup exec a build-capable environment also makes is answered
// with "no such cgroup" rather than the same body: the two execs read different
// cgroups, so answering both alike would parse the runtime container's counters
// as the build's and put a fabricated build figure in these goldens. A scenario
// that is about the build reading supplies its own via
// stubUsageKubectlExecWithBuildCgroup.
func stubUsageKubectlExec(t testing.TB, stubsDir string, lines []string) {
	t.Helper()
	stubUsageKubectlExecWithBuildCgroup(t, stubsDir, lines, []string{"build_cgroup_dir_missing=1"})
}

// stubUsageKubectlExecWithBuildCgroup answers both cgroups a build-capable
// environment's usage reading asks for: the runtime container's, and the build
// cap cgroup's, which is read through a separate exec into the erun-dind
// sidecar. The two are distinguishable only by the container the exec names,
// so the build branch has to match that first -- a stub that answered both
// with the same body would silently hide the very read this scenario exists to
// exercise.
func stubUsageKubectlExecWithBuildCgroup(t testing.TB, stubsDir string, runtimeLines, buildLines []string) {
	t.Helper()
	quote := func(lines []string) []string {
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			out = append(out, `    printf '%s\n' '`+line+`'`)
		}
		return out
	}
	body := []string{`case "$*" in`, `  *" -c erun-dind "*)`}
	body = append(body, quote(buildLines)...)
	body = append(body, `    ;;`, `  *" exec "*)`)
	body = append(body, quote(runtimeLines)...)
	body = append(body, `    ;;`, `esac`, `exit 0`)
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", strings.Join(body, "\n"))
}
