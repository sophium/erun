package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorPruneScenarios(t *testing.T) {
	t.Run("dry_run_prune_images_traces_dind_exec", testDoctorDryRunPruneImagesTracesDindExec)
	t.Run("real_run_prune_images_and_build_cache_via_stubs", testDoctorRealRunPruneImagesAndBuildCacheViaStubs)
	t.Run("real_run_prune_host_env_acts_on_this_machines_daemon", testDoctorRealRunPruneHostEnvActsOnThisMachinesDaemon)
	t.Run("real_run_prune_refuses_an_environment_with_no_build_daemon", testDoctorRealRunPruneRefusesAnEnvironmentWithNoBuildDaemon)
	t.Run("real_run_prune_reports_a_prune_that_freed_nothing", testDoctorRealRunPruneReportsAPruneThatFreedNothing)
	t.Run("real_run_without_tty_skips_optional_prune_prompts", testDoctorRealRunWithoutTtySkipsOptionalPrunePrompts)
	t.Run("real_run_without_tty_declines_pending_helm_recovery", testDoctorRealRunWithoutTtyDeclinesPendingHelmRecovery)
	t.Run("real_run_clear_pending_helm_via_prompt_then_prune_containers", testDoctorRealRunClearPendingHelmViaPromptThenPruneContainers)
}

func testDoctorDryRunPruneImagesTracesDindExec(t *testing.T) {
	// Exercises doctor.go --prune-images action: --dry-run must trace
	// the kubectl wait + the dind exec command line that would prune
	// docker images, including the resolved kubernetes context and
	// namespace, without performing any side effect.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_prune_images_traces_dind_exec", normalize.Apply(result.Combined))
}

func testDoctorRealRunPruneImagesAndBuildCacheViaStubs(t *testing.T) {
	// Real-run doctor cleanup with a healthy release: covers the
	// non-dry-run arms of runDeployDiagnosis (helm status + pods
	// rendered, guidance line), RecommendedDeployRecovery declining
	// to offer a recovery on STATUS: deployed, the real
	// traceAndWaitForRuntime/RunTracedRuntimeContainerCommand path
	// through kubectl, and two doctor actions (prune images + build
	// cache) printing their dind output via writeDoctorCommandOutput.
	// helm/kubectl are stubbed to drive the side effects the dry-run
	// trace stops short of (per erun-integration/AGENTS.md, real-run
	// scenarios via stubs).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images", "--prune-build-cache"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_prune_images_and_build_cache_via_stubs", normalize.Apply(result.Combined))
}

func testDoctorRealRunPruneHostEnvActsOnThisMachinesDaemon(t *testing.T) {
	// A host env is a worktree on this machine with no pod, so its builds
	// run against this machine's docker daemon: a prune it asks for must
	// act on that daemon and say so. The kubectl stub answers the way a
	// machine with no cluster does, so the run also proves the read and
	// the prune no longer wait on a pod (and on a cluster) this env does
	// not have.
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	state := t.TempDir()
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlNoCluster(t, stubs)
	stubDoctorHostDocker(t, stubs)
	stubDoctorHostDf(t, stubs)
	envVars := append(setup.Env(),
		append(fixture.StubEnv(stubs, "helm", "kubectl", "docker", "df"), "ERUN_STUB_STATE="+state)...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_prune_host_env_acts_on_this_machines_daemon", normalize.Apply(result.Combined))
}

func testDoctorRealRunPruneRefusesAnEnvironmentWithNoBuildDaemon(t *testing.T) {
	// A runtime env installs published versions and never builds, so it has
	// no erun-dind sidecar and no daemon holding build images. A prune asked
	// for there must fail naming that -- not run against whatever container
	// the doctor's own context happens to reach and print docker's success
	// for it. The kubectl stub fails loudly on any dind exec, so a prune
	// that still dispatched one surfaces as that failure.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlFailsOnDindExec(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("a prune that cannot run must not exit 0: %s", result.Combined)
	}
	if strings.Contains(result.Combined, "dispatched a dind exec anyway") {
		t.Fatalf("doctor pruned a daemon that does not hold this environment's build images:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/real_run_prune_refuses_an_environment_with_no_build_daemon", normalize.Apply(result.Combined))
}

func testDoctorRealRunPruneReportsAPruneThatFreedNothing(t *testing.T) {
	// The reported failure: the operator follows the build's own
	// remedy, the prune prints docker's success, and the disk does not move.
	// The stub answers with the report's own numbers -- 11.32GB reclaimable
	// before and after, docker claiming 4.105MB reclaimed -- so the run must
	// name the daemon it pruned and say plainly that the reclaim it printed
	// did not free the space the build needs, instead of leaving docker's
	// "Total reclaimed space" as the only verdict.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlPruneReclaimsNothing(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_prune_reports_a_prune_that_freed_nothing", normalize.Apply(result.Combined))
}

func testDoctorRealRunWithoutTtySkipsOptionalPrunePrompts(t *testing.T) {
	// Regression coverage for the no-TTY run: doctor is the command reached
	// for when a deploy has already failed, so its caller is often an
	// orchestrator or CI step with no terminal. Reaching the optional prune
	// prompts with stdin bound to /dev/null read EOF and exited 1 with
	// "Doctor failed team/dev: ^D" -- a verdict on an environment nothing
	// was wrong with. With no TTY each optional prune must be reported as
	// skipped, named, and left unrun, and the run must exit 0 on the health
	// of what it did examine.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev"}, erun.RunOptions{
		Cwd:              setup.Cwd,
		Env:              envVars,
		StdinFromDevNull: true,
	})
	if result.ExitCode != 0 {
		t.Fatalf("no-TTY doctor exited %d; a skipped optional prompt is not a failed environment: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "Doctor failed") {
		t.Fatalf("no-TTY doctor reported the environment as failed: %s", result.Combined)
	}
	golden.Equal(t, "doctor/real_run_without_tty_skips_optional_prune_prompts", normalize.Apply(result.Combined))
}

func testDoctorRealRunWithoutTtyDeclinesPendingHelmRecovery(t *testing.T) {
	// The other prompt doctor offers unasked: a stuck pending release makes
	// RecommendedDeployRecovery suggest one mutating recovery. With no TTY
	// that must read as "not confirmed", named, and never as a failed
	// environment -- doctor is reached for precisely when a deploy failed,
	// so a missing terminal must not cost the caller the diagnosis.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "pending-install")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev"}, erun.RunOptions{
		Cwd:              setup.Cwd,
		Env:              envVars,
		StdinFromDevNull: true,
	})
	if result.ExitCode != 0 {
		t.Fatalf("no-TTY doctor exited %d on a pending release: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "Doctor failed") {
		t.Fatalf("no-TTY doctor reported the environment as failed: %s", result.Combined)
	}
	// The diagnosis recommends exactly one recovery, so the flag named is
	// the one that fits it -- not both alternatives.
	if !strings.Contains(result.Combined, "--clear-pending-helm") {
		t.Errorf("report does not name --clear-pending-helm as the way to run the recommended recovery explicitly:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "Could not confirm") && !strings.Contains(result.Combined, "Not run") {
		t.Errorf("report does not say the recovery went unconfirmed:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "Running: Clear pending helm release") {
		t.Errorf("a recovery nobody confirmed was run:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "==> Doctor done team/dev") {
		t.Errorf("doctor did not complete the run:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/real_run_without_tty_declines_pending_helm_recovery", normalize.Apply(result.Combined))
}

func testDoctorRealRunClearPendingHelmViaPromptThenPruneContainers(t *testing.T) {
	// The helm stub reports STATUS: pending-install, so the
	// diagnosis recommends exactly one recovery and the interactive
	// confirm (the single prompt of this subprocess) accepts it:
	// covers selectedDeployRecoveryActions' prompt arm,
	// RecommendedDeployRecovery's pending-install match, and
	// RunDeployRecovery's real clear-pending arm
	// (ClearHelmReleasePendingOperation through the kubectl stub).
	// --prune-containers then exercises the third doctor action for
	// real without adding a second prompt.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "pending-install")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-containers"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "y\n",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_clear_pending_helm_via_prompt_then_prune_containers", normalize.Apply(result.Combined))
}

// stubDoctorKubectlFailsOnDindExec answers every read-only doctor surface
// normally and fails loudly on a dind exec, so a prune that dispatches one is
// visible as itself rather than as a quiet success.
func stubDoctorKubectlFailsOnDindExec(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"docker system df"*|*"df -h /var/lib/docker"*|*"docker image prune"*|*"docker builder prune"*|*"docker container prune"*) printf '%s\n' 'dispatched a dind exec anyway' >&2; exit 1 ;;`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *" exec "*) printf '%s\n' 'git push access: no credential found' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

// stubDoctorKubectlNoCluster answers kubectl the way a machine with no cluster
// does -- the shape a host environment's own machine has -- while keeping every
// read-only surface that does not need the cluster intact.
func stubDoctorKubectlNoCluster(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`echo 'error: no configuration has been provided, try setting KUBERNETES_MASTER environment variable' >&2`,
		`exit 1`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

// stubDoctorHostDocker answers the docker CLI a host environment's daemon is
// reached through. The store reading is stateful the way a real prune is: the
// first read reports images to reclaim, the prune removes them, and the read
// after it reports none -- which is what the run's own before/after has to
// relay.
func stubDoctorHostDocker(t *testing.T, stubsDir string) {
	t.Helper()
	before := []string{
		`'TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE'`,
		`'Images          21        0         11.32GB   11.32GB (100%)'`,
		`'Build Cache     0         0         0B        0B'`,
	}
	after := []string{
		`'TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE'`,
		`'Images          0         0         0B        0B'`,
		`'Build Cache     0         0         0B        0B'`,
	}
	script := strings.Join([]string{
		`reads="${ERUN_STUB_STATE}/df-reads"`,
		`count=0`,
		`[ -f "$reads" ] && count=$(cat "$reads")`,
		`case "$*" in`,
		`  *"system df --format"*)`,
		`    printf '%s\n' "$((count + 1))" >"$reads"`,
		`    if [ "$count" -eq 0 ]; then`,
		`      printf '%s\n' 'Images|11.32GB|11.32GB (100%)' 'Build Cache|0B|0B'`,
		`    else`,
		`      printf '%s\n' 'Images|0B|0B' 'Build Cache|0B|0B'`,
		`    fi`,
		`    ;;`,
		// The tables a real run shows: the inspection's read is the store as it
		// stands, and the prune action's own trailing table is the store after
		// the prune it just ran (two machine-readable reads in).
		`  *"system df"*)`,
		`    if [ "$count" -ge 2 ]; then`,
		`      printf '%s\n' ` + strings.Join(after, " ") + ``,
		`    else`,
		`      printf '%s\n' ` + strings.Join(before, " ") + ``,
		`    fi`,
		`    ;;`,
		`  *"image prune"*) printf '%s\n' 'Deleted Images:' 'Total reclaimed space: 11.32GB' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "docker", script)
}

// stubDoctorHostDf answers the reads of the daemon's own root directory, which
// a host machine's docker keeps somewhere it can be stat'd (unlike the dind
// sidecar's, which is inside that container).
func stubDoctorHostDf(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"-h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Use%  Mounted on' '/dev/vda1   200G  180G  11G    95%   /var/lib/docker' ;;`,
		`  *"-i /var/lib/docker"*) printf '%s\n' 'Filesystem  Inodes  IUsed  IFree  IUse%  Mounted on' '/dev/vda1   13M    1.2M   11.8M  9%     /var/lib/docker' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "df", script)
}

// stubDoctorKubectlPruneReclaimsNothing answers a prune's dind exec with the
// reported shape: docker prints a reclaim (4.105MB) while the store
// it pruned still reports the same 11.32GB reclaimable, which is a prune that
// freed none of the space the build needs. Every other doctor surface keeps the
// default stub's answers.
func stubDoctorKubectlPruneReclaimsNothing(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *" wait "*) : ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		`  *"erun-doctor-read:before"*)`,
		`    printf '%s\n' \`,
		`      'erun-doctor-read:before' \`,
		`      'Images|11.32GB|11.32GB (100%)' \`,
		`      'Build Cache|0B|0B' \`,
		`      'erun-doctor-read:before:end' \`,
		`      'Total reclaimed space: 4.105MB' \`,
		`      'erun-doctor-read:after' \`,
		`      'Images|11.32GB|11.32GB (100%)' \`,
		`      'Build Cache|0B|0B' \`,
		`      'erun-doctor-read:after:end' \`,
		`      '== Docker system df ==' \`,
		`      'Images          21        0         11.32GB   11.32GB (100%)' \`,
		`      'Build Cache     0         0         0B        0B'`,
		`    ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}
