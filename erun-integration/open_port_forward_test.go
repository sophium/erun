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
	// EnvConfig.LocalPortRangeStart is the durable per-env port
	// contract. When it is already set on disk, open must derive every
	// service port from it (MCP 17500, API 17533, SSH 17522) and must
	// not emit the "config: would assign" trace. Locks the early-return
	// in EnsureLocalPortRangePersisted (open.go::common helper).
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17500)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/persisted_local_port_range_is_honoured", normalize.Apply(result.Combined))
}

func testOpenWalkerSkipsIndexClaimedByOtherTenant(t *testing.T) {
	t.Parallel()
	// When a second env on the host has already persisted
	// localportrangestart=17000, the alphabetical walker must skip
	// that index when allocating an unpersisted env. team/dev sorts
	// before other/staging but the persisted claim wins, so team/dev
	// resolves to 17100 and the dry-run trace records the would-be
	// assignment. Locks the cross-tenant claim-respect branch in
	// ResolveAllEnvironmentLocalPorts.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 17000)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/walker_skips_index_claimed_by_other_tenant", normalize.Apply(result.Combined))
}

func testOpenOverlappingPersistedRangesFailWithPointer(t *testing.T) {
	t.Parallel()
	// Two envs that persist the same localportrangestart must surface
	// an ErrLocalPortRangeOverlap pointing at both. The CLI should
	// fail with a non-zero exit and a message naming both envs and the
	// range, instead of silently re-allocating one of them. Locks the
	// overlap path in ResolveAllEnvironmentLocalPorts.
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17000)
	fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 17000)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/overlapping_persisted_ranges_fail_with_pointer", normalize.Apply(result.Combined))
}

func testOpenMisalignedPersistedRangeFailsWithPointer(t *testing.T) {
	t.Parallel()
	// localportrangestart must align to EnvironmentPortRangeSize=100
	// boundaries from LowerServicePort=17000. A value like 17050
	// would make MCP/API/SSH offsets bleed into another env's range,
	// so the resolver refuses with a clear alignment error instead of
	// accepting it. Locks environmentPortIndexForRangeStart's
	// validation branch.
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17050)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/misaligned_persisted_range_fails_with_pointer", normalize.Apply(result.Combined))
}

func testOpenAdoptsExistingKubectlPortForward(t *testing.T) {
	// Regression for the orphan-kubectl-can't-adopt failure: when the
	// per-env state file is missing but a long-lived `kubectl
	// port-forward` is already serving the env's MCP port, erun must
	// recognise the holder as its own and reuse it instead of erroring
	// with "already in use". The probe runs in dry-run too, so we lock
	// the decision in the golden via the "would adopt" trace.
	//
	// The holder is a real listener that answers, not just an lsof claim:
	// adoption now requires the tunnel to carry traffic, so a stubbed
	// holder alone would leave the decision to whatever else happens to
	// hold that port on the host. That pins the scenario to the 26100
	// range, like every other one that binds a port.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	// Pin DetectHost to darwin so the --no-shell preamble stays POSIX (see
	// openHostOSOverride); the adopt holder probe is stubbed, so the pinned OS
	// only keeps the golden deterministic across hosts.
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
	// The sibling decision, and the reason the one above needs a live
	// holder at all: a holder with erun's exact port-forward argv whose
	// far end is gone still binds the port and answers nothing through
	// it. Adopting it would hand the caller a tunnel to a pod that no
	// longer exists — the failure that left an environment unreachable
	// for hours behind a listener that looked healthy. The plan must say
	// it would replace the forward, and dry-run must stop at saying so:
	// the holder is still bound when the run ends.
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
	// Side effect outside the captured streams: dry-run plans the
	// replacement, it does not perform it.
	if fixture.StalePortHolderStopped(26100, 500*time.Millisecond) {
		t.Error("dry-run must leave the stale holder running; it only plans the replacement")
	}
}

func testOpenRealRunReestablishesABoundButDeadPortForward(t *testing.T) {
	// The reported failure, verbatim: erun's own recorded forward is
	// still bound — its state file matches this env and its process is
	// alive — while every request through it dies, because the pod it
	// targeted was replaced. Reusing it on the strength of the recorded
	// state is what let that survive for five hours, so open must stop
	// the dead forward and start a fresh one that answers.
	//
	// The holder is a real process and the ps/lsof stubs name its real
	// PID: production kills what the probe names, so a fabricated PID
	// would aim the kill at whatever else owns that number.
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
	// ERUN_FORCE_TTY=1: this scenario locks the interactive shell path (the
	// form an operator at a real terminal runs); without it, stdin's
	// non-TTY default in this harness would take the no-shell branch instead.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	holderPID := fixture.StartStalePortHolder(t, 26100)
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 26100, pid: holderPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)
	writePortForwardState(t, setup, "mcp", "team", "dev", 26100, holderPID)

	// The shell form, not --no-shell: real-run --no-shell silences stderr
	// so an `eval "$(erun open ...)"` alias stays quiet, and the decision
	// this scenario exists to lock is a trace line. The exec stub exits 0
	// so runShellLoop ends after one pass.
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/real_run_reestablishes_a_bound_but_dead_port_forward", normalize.Apply(result.Combined))
	// Side effects outside the captured streams: the dead forward is
	// gone, and the recorded forward is a different process — the same
	// PID would mean erun re-adopted the corpse.
	stateBody, err := os.ReadFile(portForwardStateFile(setup, "mcp", "team", "dev"))
	if err != nil {
		t.Fatalf("read rewritten mcp state: %v", err)
	}
	if strings.Contains(string(stateBody), fmt.Sprintf(`"processId":%d`, holderPID)) {
		t.Errorf("expected the state file to record a fresh forward, still claims the dead PID %d:\n%s", holderPID, stateBody)
	}
}

func testOpenNoShellRealRunRefusesSuccessWhenAStaleForwardCannotBeStopped(t *testing.T) {
	// Regression for the defect: erun's own error already names this exact
	// shape ("127.0.0.1:PORT is held but the edge never answers -- a
	// stale port-forward") and tells the operator to run `erun open`,
	// but running it silently warned and exited 0 while the edge stayed
	// unreachable -- the reported failure, verbatim. The detect-and-
	// replace machinery (adoptForeignMCPPortForward /
	// replaceStalePortForwardHolder) is tried regardless, and when the
	// stale holder genuinely cannot be stopped, `open --no-shell` has no
	// shell to fall back on, so it must fail loudly instead of reporting
	// success on a port nothing is serving.
	//
	// The lsof/ps probes present a PID that names no real process, so
	// production's kill of it fails with ESRCH -- simulating a holder
	// erun cannot actually stop -- while a real listener (a different
	// PID, from StartStalePortHolder) keeps the port bound and silent so
	// the initial reachability probe genuinely observes "held but dead".
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
	// A `kubectl port-forward` that is still retrying against a pod that
	// never answers holds no port at all — unlike the bound-but-dead shape
	// above, nothing binds, but the process itself is alive. Bound state
	// alone can't tell that corpse apart from one that already exited, so
	// the plan must still name it. Dry-run only plans: the process must
	// still be alive when the run ends.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	deadPID := fixture.StartUnboundPortForwardProcess(t)
	// port: 0 keeps the fabricated holder out of the lsof answer for
	// 26100 (nothing is really bound), while the ps stub still answers
	// for this exact PID — the shape production's isPortForwardProcess
	// check needs to recognise the recorded PID as its own.
	stubAdoptHolderProbes(t, setup, adoptHolder{
		port: 0, pid: deadPID,
		argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
	})
	writePortForwardState(t, setup, "mcp", "team", "dev", 26100, deadPID)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/previews_reaping_a_recorded_forward_that_never_bound_its_port", normalize.Apply(result.Combined))
	// Side effect outside the captured streams: dry-run plans the reap,
	// it does not perform it.
	if fixture.ProcessStopped(deadPID, 500*time.Millisecond) {
		t.Error("dry-run must leave the never-bound forward running; it only plans clearing it")
	}
}

func testOpenRealRunReapsARecordedForwardThatNeverBoundItsPort(t *testing.T) {
	// The real-run sibling, and the reported failure itself: a forward
	// that never bound its port is never reaped, so every repeat attempt
	// starts a fresh kubectl beside the last one — 101 of them for one
	// environment in the reported case. open must kill the recorded PID
	// before starting a replacement, not leave it running as one more
	// corpse nothing will ever clear.
	//
	// The holder is a real process and the ps/lsof stubs name its real
	// PID: production kills what the probe names, so a fabricated PID
	// would aim the kill at whatever else owns that number.
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
	// The race the recorded-PID reap alone cannot close: two overlapping
	// opens for the same environment can each read the old state file
	// before either overwrites it, so the invocation that loses that
	// race leaves a kubectl process with no state entry left pointing at
	// it. This scenario reproduces the same end state without needing
	// real concurrency — no state file names this env's forward at
	// all — so only the argv-identity sweep (not the state-based reap)
	// can find and clear the orphan.
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
	// No writePortForwardState call: the sweep must find this PID by
	// argv identity alone, with no state file naming it at all.

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
	// When the port is held by a process whose argv does not look like
	// the kubectl port-forward erun would start, adoption is unsafe.
	// erun must trace what is holding the port (PID + argv) so the
	// user sees what to kill, instead of the bare "already in use"
	// message that gave nothing actionable in the prior UI loop.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	// Pin DetectHost to darwin so the --no-shell preamble stays POSIX (see
	// openHostOSOverride); the adopt holder probe is stubbed, so the pinned OS
	// only keeps the golden deterministic across hosts.
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
	// Regression for the API forward on any environment whose api port is
	// not the canonical 17033. `erun deploy` publishes the erun-backend-api
	// Service on the environment's own api port (the chart's apiPort comes
	// from the same per-env allocation), so a forward whose far side is the
	// APIServicePort constant asks kubectl for a service port the Service
	// does not have: kubectl answers "Service team-api does not have a
	// service port 17033", and erun open times out with no local channel.
	//
	// The env is seeded in the 26100 range so its api port is 26133, and the
	// foreign-holder refusal is what makes dry-run print the API forward's
	// own argv — the value the deployed Service port has to match. The
	// holder is stubbed (lsof/ps), so nothing is bound and the ports need
	// not be free.
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
	// Regression for the --intellij short-circuit on tenant-owned
	// devops charts. The runtime-pod identity matcher must accept a
	// deployment whose containers expose ERUN_REPO_PATH,
	// ERUN_SSHD_ENABLED, ERUN_MCP_PORT, and ERUN_SSHD_PORT but not
	// ERUN_API_PORT — the erun-api service is a separate deployment
	// with its own port-forward path, so the runtime pod legitimately
	// omits that env var. Without the fix, every open against a
	// tenant chart predating ERUN_API_PORT would be flagged as "not
	// deployed" and either redeploy unnecessarily (shell flow) or
	// hard-error (--intellij / --vscode flow).
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
	// Keep the adopt-or-conflict probe silent: this scenario is about
	// the deployment-match path, not orphan adoption, and we don't
	// want the developer's host state to leak a "would refuse" line
	// into the golden when port 17000 is in use locally.
	stubLsofNoHolder(t, stubsDir)
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)
	// Pin DetectHost so the --no-shell preamble dialect stays POSIX; see
	// openHostOSOverride.
	envVars = append(envVars, openHostOSOverride)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/deployment_match_ignores_missing_api_port", normalize.Apply(result.Combined))
}
