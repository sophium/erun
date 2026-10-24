package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testOpenPersistedLocalPortRangeIsHonoured(t *testing.T) {
	t.Parallel()
	// A persisted localportrangestart drives every service port, with no assign trace.
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17500)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/persisted_local_port_range_is_honoured", normalize.Apply(result.Combined))
}

func testOpenWalkerSkipsIndexClaimedByOtherTenant(t *testing.T) {
	t.Parallel()
	// Another env's persisted range is skipped when allocating an unpersisted one.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 17000)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/walker_skips_index_claimed_by_other_tenant", normalize.Apply(result.Combined))
}

func testOpenOverlappingPersistedRangesFailWithPointer(t *testing.T) {
	t.Parallel()
	// Duplicate persisted ranges fail naming both envs, never silently reallocate.
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17000)
	fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 17000)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/overlapping_persisted_ranges_fail_with_pointer", normalize.Apply(result.Combined))
}

func testOpenMisalignedPersistedRangeFailsWithPointer(t *testing.T) {
	t.Parallel()
	// Unaligned range starts would bleed into another env's range, so they are refused.
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17050)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/misaligned_persisted_range_fails_with_pointer", normalize.Apply(result.Combined))
}

func testOpenAdoptsExistingKubectlPortForward(t *testing.T) {
	// An orphan kubectl forward that actually carries traffic is adopted. The
	// holder is a real listener because adoption requires a live tunnel.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	// Pin darwin so the --no-shell preamble stays POSIX and the golden deterministic.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	holderPID := fixture.StartServingPortHolder(t, 26100)
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 26100, pid: holderPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/adopts_existing_kubectl_port_forward", normalize.Apply(result.Combined))
}

func testOpenPreviewsReplacingABoundButDeadPortForward(t *testing.T) {
	// A holder with erun's argv whose far end is gone must be replaced, not
	// adopted; dry-run only plans the replacement.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	holderPID := fixture.StartStalePortHolder(t, 26100)
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 26100, pid: holderPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/previews_replacing_a_bound_but_dead_port_forward", normalize.Apply(result.Combined))
	if fixture.StalePortHolderStopped(26100, 500*time.Millisecond) {
		t.Error("dry-run must leave the stale holder running; it only plans the replacement")
	}
}

func testOpenRealRunReestablishesABoundButDeadPortForward(t *testing.T) {
	// erun's own recorded forward to a replaced pod must be stopped and restarted.
	// The stubs name the holder's real PID because production kills what the probe names.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
	})...)
	envVars = append(envVars, openHostOSOverride)
	// ERUN_FORCE_TTY=1 takes the interactive shell path.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	holderPID := fixture.StartStalePortHolder(t, 26100)
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 26100, pid: holderPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)
	writePortForwardState(t, setup, "mcp", "team", "dev", 26100, holderPID)

	// Not --no-shell, which silences the stderr trace this asserts. The exec stub
	// exits 0 so runShellLoop ends after one pass.
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/real_run_reestablishes_a_bound_but_dead_port_forward", normalize.Apply(result.Combined))
	// A same PID would mean erun re-adopted the dead forward.
	stateBody, err := os.ReadFile(portForwardStateFile(setup, "mcp", "team", "dev"))
	if err != nil {
		t.Fatalf("read rewritten mcp state: %v", err)
	}
	if strings.Contains(string(stateBody), fmt.Sprintf(`"processId":%d`, holderPID)) {
		t.Errorf("expected the state file to record a fresh forward, still claims the dead PID %d:\n%s", holderPID, stateBody)
	}
}

func testOpenNoShellRealRunRefusesSuccessWhenAStaleForwardCannotBeStopped(t *testing.T) {
	// A stale holder that cannot be stopped must fail `open --no-shell` loudly.
	// The probes name a nonexistent PID so the kill fails with ESRCH while a real
	// listener keeps the port bound and silent.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
	})...)
	envVars = append(envVars, openHostOSOverride)
	fixture.StartStalePortHolder(t, 26100)
	const unkillablePID = 999999999
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 26100, pid: unkillablePID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)

	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the stale port-forward could not be replaced, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "open/no_shell_real_run_refuses_success_when_a_stale_forward_cannot_be_stopped", normalize.Apply(result.Combined))
}

func testOpenPreviewsReapingARecordedForwardThatNeverBoundItsPort(t *testing.T) {
	// A forward still retrying binds nothing but is alive; the plan must still name it.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	deadPID := fixture.StartUnboundPortForwardProcess(t)
	// port: 0 keeps the holder out of lsof while ps still recognises the PID.
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 0, pid: deadPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	writePortForwardState(t, setup, "mcp", "team", "dev", 26100, deadPID)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/previews_reaping_a_recorded_forward_that_never_bound_its_port", normalize.Apply(result.Combined))
	if fixture.ProcessStopped(deadPID, 500*time.Millisecond) {
		t.Error("dry-run must leave the never-bound forward running; it only plans clearing it")
	}
}

func testOpenRealRunReapsARecordedForwardThatNeverBoundItsPort(t *testing.T) {
	// A recorded forward that never bound must be killed before starting a
	// replacement. The stubs name the real PID because production kills it.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
	})...)
	envVars = append(envVars, openHostOSOverride)
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	deadPID := fixture.StartUnboundPortForwardProcess(t)
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 0, pid: deadPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)
	writePortForwardState(t, setup, "mcp", "team", "dev", 26100, deadPID)

	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/real_run_reaps_a_recorded_forward_that_never_bound_its_port", normalize.Apply(result.Combined))
	if !fixture.ProcessStopped(deadPID, 2*time.Second) {
		t.Errorf("expected the never-bound forward (PID %d) to be killed before starting a fresh one", deadPID)
	}
	stateBody, err := os.ReadFile(portForwardStateFile(setup, "mcp", "team", "dev"))
	if err != nil {
		t.Fatalf("read rewritten mcp state: %v", err)
	}
	if strings.Contains(string(stateBody), fmt.Sprintf(`"processId":%d`, deadPID)) {
		t.Errorf("expected the state file to record a fresh forward, still claims the dead PID %d:\n%s", deadPID, stateBody)
	}
}

func testOpenRealRunSweepsAnOrphanedForwardWithNoMatchingState(t *testing.T) {
	// Overlapping opens can orphan a kubectl with no state entry; only the
	// argv-identity sweep can clear it.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
	})...)
	envVars = append(envVars, openHostOSOverride)
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	orphanPID := fixture.StartUnboundPortForwardProcess(t)
	orphanArgv := "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1"
	stubLsofNoHolder(t, stubsDir)
	fixture.StubBinaryWithScript(t, stubsDir, "ps", fmt.Sprintf(`for arg in "$@"; do
    if [ "$arg" = "-e" ]; then
        printf '%%s %%s\n' %d %s
        exit 0
    fi
    if [ "$arg" = "%d" ]; then
        printf '%%s\n' %s
        exit 0
    fi
done
exit 1
`, orphanPID, shellQuote(orphanArgv), orphanPID, shellQuote(orphanArgv)))
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)

	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/real_run_sweeps_an_orphaned_forward_with_no_matching_state", normalize.Apply(result.Combined))
	if !fixture.ProcessStopped(orphanPID, 2*time.Second) {
		t.Errorf("expected the unrecorded orphan (PID %d) to be swept before starting a fresh forward", orphanPID)
	}
}

func testOpenRefusesToBindWhenForeignProcessHoldsPort(t *testing.T) {
	t.Parallel()
	// A non-kubectl holder is not adopted; its PID and argv are traced.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	// Pin darwin so the --no-shell preamble stays POSIX and the golden deterministic.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 17000, pid: 88888,
		argv: "/usr/local/bin/some-foreign-process --bind 127.0.0.1:17000",
	})
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/refuses_to_bind_when_foreign_process_holds_port", normalize.Apply(result.Combined))
}

func testOpenApiForwardTargetsTheEnvironmentsOwnServicePort(t *testing.T) {
	t.Parallel()
	// The API forward must target the env's own api port (26133 here), which is
	// the port deploy publishes on the Service, not the APIServicePort constant.
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 26133, pid: 88889,
		argv: "/usr/local/bin/some-foreign-process --bind 127.0.0.1:26133",
	})
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/api_forward_targets_the_environments_own_service_port", normalize.Apply(result.Combined))
}

func testOpenDeploymentMatchIgnoresMissingApiPort(t *testing.T) {
	t.Parallel()
	// The runtime pod legitimately omits ERUN_API_PORT, so its absence must not
	// read as "not deployed".
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        17000,
		SSHPort:        17022,
	})...)
	// Keep host port state out of the golden.
	stubLsofNoHolder(t, stubsDir)
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)
	envVars = append(envVars, openHostOSOverride)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/deployment_match_ignores_missing_api_port", normalize.Apply(result.Combined))
}
