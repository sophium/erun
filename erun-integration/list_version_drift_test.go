package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListVersionDriftGateEnvironmentRequiresTenant(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"list", "--gate-environment", "build"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --gate-environment with no --tenant, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/version_drift_gate_environment_requires_tenant", normalize.Apply(result.Combined))
}

func testListVersionDriftTenantNotFound(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"list", "--tenant", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unknown tenant, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/version_drift_tenant_not_found", normalize.Apply(result.Combined))
}

func testListVersionDriftReportsEveryEnvironmentAndTheMax(t *testing.T) {
	// Two environments in one tenant on different erun versions must surface
	// the drift. build stays behind at 1.0.246 while code4 already runs 1.0.247.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_reports_every_environment_and_the_max", normalize.Apply(result.Combined))
}

func testListVersionDriftGateEnvironmentBehindFlagsWhatItGates(t *testing.T) {
	// The real defect: the gate environment (build) is older than an
	// environment it gates (code4), and that must read as behind=yes,
	// naming code4.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--gate-environment", "build"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_gate_environment_behind_flags_what_it_gates", normalize.Apply(result.Combined))
}

func testListVersionDriftGateEnvironmentUpToDate(t *testing.T) {
	// The counterpart: the gate environment already carries the newest
	// version observed, so behind must read no.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--gate-environment", "code4"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_gate_environment_up_to_date", normalize.Apply(result.Combined))
}

func testListVersionDriftGateEnvironmentNotFound(t *testing.T) {
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--gate-environment", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unknown gate environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/version_drift_gate_environment_not_found", normalize.Apply(result.Combined))
}

func testListVersionDriftGateEnvironmentVersionUnresolved(t *testing.T) {
	// The gate environment has never recorded a resolved runtime image, so
	// its own erun version cannot be read from config alone -- that gap
	// now falls back to a live helm read rather than assuming
	// "none" -- stubbed here as a confirmed absence (no release at all),
	// so behind must still read "unknown" naming why, never a silent "no"
	// that would misreport an unknown gate as safely current.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "gate")
	seedTenantEnvOnErunLine(t, setup, "team", "peer", "1.0.247")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{Stderr: `Error: release: not found`, ExitCode: 1})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"list", "--tenant", "team", "--gate-environment", "gate"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_gate_environment_version_unresolved", normalize.Apply(result.Combined))
}

func testListVersionDriftEnvironmentConfirmedNotDeployed(t *testing.T) {
	// A live check now backs "none": it is a confirmed absence, not a
	// guess from an empty config field. code4 has never been deployed
	// anywhere, and the live helm read confirms exactly that.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.247")
	fixture.SeedTenantEnv(t, setup, "erun", "code4")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{Stderr: `Error: release: not found`, ExitCode: 1})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"list", "--tenant", "erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_environment_confirmed_not_deployed", normalize.Apply(result.Combined))
}

func testListVersionDriftResolvesLiveVersionFromDeployedRelease(t *testing.T) {
	// local's own config never recorded a runtime version (e.g. it was
	// deployed from a different machine or session), but its cluster is
	// reachable and running 1.0.203 -- newer than build's cached 1.0.100.
	// The live read must resolve it, fold it into the tenant's own max,
	// and flag build as behind, not silently report local as "none".
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.100")
	fixture.SeedTenantEnv(t, setup, "erun", "local")
	stubs := setup.Cwd + "/stubs"
	fixture.StubHelmObserve(t, stubs, observeHelmStatusStub(), observeHelmListStub("erun-devops-1.0.203", "1.0.203"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"list", "--tenant", "erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_resolves_live_version_from_deployed_release", normalize.Apply(result.Combined))
}

func testListVersionDriftEnvironmentClusterUnreachable(t *testing.T) {
	// petios's cluster cannot be reached at all -- distinct from both a
	// confirmed absence and a resolved version, and excluded from the max
	// / behind computation with the exclusion stated, not dropped silently.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.247")
	fixture.SeedTenantEnv(t, setup, "erun", "petios")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{
		Stderr:   `Error: Kubernetes cluster unreachable: dial tcp 10.0.0.5:6443: i/o timeout`,
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"list", "--tenant", "erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_environment_cluster_unreachable", normalize.Apply(result.Combined))
}

func testListFailOnDriftWithUnresolvedEnvironmentExitsNonZero(t *testing.T) {
	// A drift check that cannot tell whether an environment is behind must
	// not pass silently: --fail-on-drift treats an unresolved environment
	// the same as one confirmed behind.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.247")
	fixture.SeedTenantEnv(t, setup, "erun", "petios")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{
		Stderr:   `Error: Kubernetes cluster unreachable: dial tcp 10.0.0.5:6443: i/o timeout`,
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a tenant with an unresolved environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/fail_on_drift_with_unresolved_environment_exits_non_zero", normalize.Apply(result.Combined))
}

func testListVersionDriftDryRunTracesLiveCheckForUnresolvedEnvironment(t *testing.T) {
	// Dry run must never touch the network: no helm stub is declared here
	// at all, so if production code called helm for real this would fail
	// loudly ("executable file not found") rather than pass by accident.
	// The two helm calls that would run are traced instead, auditable
	// before anyone lets the real check dial a cluster.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.247")
	fixture.SeedTenantEnv(t, setup, "erun", "petios")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_dry_run_traces_live_check_for_unresolved_environment", normalize.Apply(result.Combined))
}

func testListVersionDriftUnresolvedEnvironmentJSONOutput(t *testing.T) {
	// Locks the JSON field names a scripted caller reads:
	// versionUnresolved/versionUnresolvedReason, distinct from a bare
	// missing "version" key (which alone cannot tell "none" apart from
	// "undetermined").
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.247")
	fixture.SeedTenantEnv(t, setup, "erun", "petios")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{
		Stderr:   `Error: Kubernetes cluster unreachable: dial tcp 10.0.0.5:6443: i/o timeout`,
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_unresolved_environment_json_output", normalize.Apply(result.Combined))
}

func testListVersionDriftJSONOutput(t *testing.T) {
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--gate-environment", "build", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/version_drift_json_output", normalize.Apply(result.Combined))
}

func testListFailOnDriftRequiresTenantOrControlPlanes(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"list", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --fail-on-drift with neither --tenant nor --control-planes, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/fail_on_drift_requires_tenant_or_control_planes", normalize.Apply(result.Combined))
}

func testListFailOnDriftWithTenantBehindMaxExitsNonZero(t *testing.T) {
	// build is behind code4; with --fail-on-drift the report itself is what a
	// schedule can gate on.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a tenant with an environment behind max, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/fail_on_drift_with_tenant_behind_max_exits_non_zero", normalize.Apply(result.Combined))
}

func testListFailOnDriftWithTenantNoDriftExitsZero(t *testing.T) {
	// The report still prints in full; --fail-on-drift changes only the
	// exit code, and only when there is something to fail on.
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.247")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 for a tenant with no environment behind max, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/fail_on_drift_with_tenant_no_drift_exits_zero", normalize.Apply(result.Combined))
}

func testListFailOnDriftWithGateEnvironmentBehindExitsNonZero(t *testing.T) {
	setup := env.New(t)
	seedTenantEnvOnErunLine(t, setup, "erun", "build", "1.0.246")
	seedTenantEnvOnErunLine(t, setup, "erun", "code4", "1.0.247")
	result := erun.Run(t, []string{"list", "--tenant", "erun", "--gate-environment", "build", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a gate environment behind what it gates, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/fail_on_drift_with_gate_environment_behind_exits_non_zero", normalize.Apply(result.Combined))
}
