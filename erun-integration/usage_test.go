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
		// interval, without executing anything. team/dev is build-capable
		// (carries the erun-dind sidecar), so a second traced exec into that
		// container must appear too -- per erun-integration/AGENTS.md's
		// dry-run contract, every action this command would take needs its
		// own trace line, and reading the sidecar is now one of them.
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
		// `sleep` line — the golden pins the flag actually reaching the plan,
		// for both the runtime container and the dind sidecar exec.
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
	// real_run_dind_sidecar_usage_reported_separately is erun#2120's own
	// regression scenario: an environment mid-release can read idle from the
	// runtime container while the erun-dind sidecar -- where the actual build
	// runs -- is close to its own memory limit, and before this fix nothing
	// in `erun usage`'s output let an operator tell the two apart. The stub
	// answers each container differently, matching the live measurement the
	// issue recorded (runtime container near-idle, sidecar under real load),
	// and asserts the sidecar's own reading -- and its own warning -- surface
	// distinctly rather than being silently dropped or conflated with the
	// runtime container's numbers.
	t.Run("real_run_dind_sidecar_usage_reported_separately", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubUsageKubectlExecPerContainer(t, stubs,
			[]string{
				"cgroup_type=cgroup2fs",
				"memory_current=104857600",
				"memory_max=24696061952",
				"memory_peak=104857600",
				"memory_oom_kill=0",
				"cpu_max=1200000 100000",
				"cpu_usage_before=1000000",
				"cpu_usage_after=1003000",
				"cpu_time_before_ns=1000000000",
				"cpu_time_after_ns=2000000000",
				"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
			},
			[]string{
				"cgroup_type=cgroup2fs",
				"memory_current=13421772800",
				"memory_max=15032385536",
				"memory_peak=13958643712",
				"memory_oom_kill=0",
				"cpu_max=400000 100000",
				"cpu_usage_before=1000000",
				"cpu_usage_after=2900000",
				"cpu_time_before_ns=1000000000",
				"cpu_time_after_ns=2000000000",
				"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
			},
		)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, `"dind"`) {
			t.Fatalf("expected a dind reading in the JSON output for a build-capable env, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, `"currentBytes": 13421772800`) {
			t.Fatalf("expected the sidecar's own (busy) memory reading distinct from the runtime container's idle one, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "erun-dind: memory is at") {
			t.Fatalf("expected the sidecar's own near-limit memory to warn distinctly from the runtime container, got:\n%s", result.Combined)
		}
		golden.Equal(t, "usage/real_run_dind_sidecar_usage_reported_separately", normalize.Apply(result.Combined))
	})

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
// script with fixed key=value lines, regardless of which container argv
// targets — the script itself only runs for real inside a live cgroup v2
// container, which this harness does not have. Every other kubectl
// invocation exits 0 silently. Since RunRuntimeUsage now execs the same
// script into both the runtime container and the erun-dind sidecar on a
// build-capable env, this answers both identically; use
// stubUsageKubectlExecPerContainer when a scenario needs them to differ.
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

// stubUsageKubectlExecPerContainer is stubUsageKubectlExec's differentiated
// sibling: the erun-dind exec (matched by the `-c erun-dind` argv it always
// carries, checked before the generic `exec` case below it) answers with
// dindLines, and every other exec (the runtime container) answers with
// runtimeLines.
func stubUsageKubectlExecPerContainer(t testing.TB, stubsDir string, runtimeLines, dindLines []string) {
	t.Helper()
	body := make([]string, 0, len(runtimeLines)+len(dindLines)+6)
	body = append(body, `case "$*" in`, `  *" -c erun-dind "*)`)
	for _, line := range dindLines {
		body = append(body, `    printf '%s\n' '`+line+`'`)
	}
	body = append(body, `    ;;`, `  *" exec "*)`)
	for _, line := range runtimeLines {
		body = append(body, `    printf '%s\n' '`+line+`'`)
	}
	body = append(body, `    ;;`, `esac`, `exit 0`)
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", strings.Join(body, "\n"))
}
