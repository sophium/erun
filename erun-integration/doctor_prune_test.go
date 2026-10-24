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
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_prune_images_traces_dind_exec", normalize.Apply(result.Combined))
}

func testDoctorRealRunPruneImagesAndBuildCacheViaStubs(t *testing.T) {
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
	// A host env prunes this machine's docker daemon; the no-cluster kubectl stub
	// proves nothing waits on a pod.
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
	// A runtime env has no dind sidecar, so a prune must fail naming that. Any dind
	// exec fails the stub.
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
	// Docker reports a reclaim while reclaimable space is unchanged: the run must say
	// the prune freed nothing.
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
	// Without a TTY each optional prune is reported skipped and the run exits 0.
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
	// Without a TTY the recommended recovery reads as not confirmed, not a failure.
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
	// One prompt per subprocess: the recovery confirm is it, and --prune-containers
	// adds none.
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

// stubDoctorKubectlFailsOnDindExec fails loudly on a dind exec.
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

// stubDoctorKubectlNoCluster answers kubectl as a machine with no cluster does.
func stubDoctorKubectlNoCluster(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`echo 'error: no configuration has been provided, try setting KUBERNETES_MASTER environment variable' >&2`,
		`exit 1`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

// stubDoctorHostDocker is stateful: images are reclaimable before the prune and gone after.
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

// stubDoctorHostDf answers reads of the host daemon's root directory.
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

// stubDoctorKubectlPruneReclaimsNothing reports a reclaim while the store stays at
// 11.32GB reclaimable.
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
