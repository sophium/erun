package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorDeployDiagnosisScenarios(t *testing.T) {
	t.Run("dry_run_reports_library_execution_mode", testDoctorDryRunReportsLibraryExecutionMode)
	t.Run("dry_run_reports_deploy_diagnosis", testDoctorDryRunReportsDeployDiagnosis)
	t.Run("dry_run_unaffected_by_kubectl_deployment_wait_library_execution_mode", testDoctorDryRunUnaffectedByKubectlDeploymentWaitLibraryExecutionMode)
	t.Run("real_run_kubectl_deployment_wait_library_execution_mode_reaches_the_api_server_directly", testDoctorRealRunKubectlDeploymentWaitLibraryExecutionModeReachesTheApiServerDirectly)
	t.Run("dry_run_rollback_traces_helm_rollback", testDoctorDryRunRollbackTracesHelmRollback)
	t.Run("real_run_rollback_failure_reports_helm_stderr", testDoctorRealRunRollbackFailureReportsHelmStderr)
	t.Run("real_run_reports_memory_pressure_and_the_sizing_verdict", testDoctorRealRunReportsMemoryPressureAndTheSizingVerdict)
	t.Run("dry_run_clear_pending_helm_traces_kubectl_delete", testDoctorDryRunClearPendingHelmTracesKubectlDelete)
	t.Run("conflicting_recovery_flags_error", testDoctorConflictingRecoveryFlagsError)
	t.Run("real_run_cluster_unreachable_skips_pod_dependent_sections_once_established", testDoctorRealRunClusterUnreachableSkipsPodDependentSectionsOnceEstablished)
	t.Run("real_run_storage_unhealthy_diagnostic_error", testDoctorRealRunStorageUnhealthyDiagnosticError)
	t.Run("real_run_unrecognized_wait_failure_still_reports_kubectl_stderr", testDoctorRealRunUnrecognizedWaitFailureStillReportsKubectlStderr)
	t.Run("real_run_unrecognized_wait_failure_with_no_stderr_reports_exit_status_plainly", testDoctorRealRunUnrecognizedWaitFailureWithNoStderrReportsExitStatusPlainly)
	t.Run("real_run_namespace_listed_but_api_failing_error", testDoctorRealRunNamespaceListedButApiFailingError)
}

func testDoctorDryRunReportsLibraryExecutionMode(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "aws-sts", "library")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_library_execution_mode", normalize.Apply(result.Combined))
}

func testDoctorDryRunReportsDeployDiagnosis(t *testing.T) {
	// --dry-run withholds mutations, not reads, so the diagnosis still prints.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "failed")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_deploy_diagnosis", normalize.Apply(result.Combined))
}

func testDoctorDryRunUnaffectedByKubectlDeploymentWaitLibraryExecutionMode(t *testing.T) {
	// Library-mode wait renders the same kubectl trace on a dry run as subprocess mode.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-deployment-wait", "library")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_unaffected_by_kubectl_deployment_wait_library_execution_mode", normalize.Apply(result.Combined))
}

func testDoctorRealRunKubectlDeploymentWaitLibraryExecutionModeReachesTheApiServerDirectly(t *testing.T) {
	// The kubectl stub fails only for "wait", so a fallback to the subprocess path
	// fails visibly.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-deployment-wait", "library")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, `printf '%s\n' "fell through to the kubectl subprocess for the wait check" >&2; exit 1`)

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/apis/apps/v1/namespaces/team-dev/deployments/team-devops" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"team-devops","namespace":"team-dev"},`+
			`"status":{"conditions":[{"type":"Available","status":"True"}]}}`)
	}))
	defer apiServer.Close()
	kubeDir := filepath.Join(setup.Home, ".kube")
	if err := os.MkdirAll(kubeDir, 0o700); err != nil {
		t.Fatalf("mkdir .kube: %v", err)
	}
	kubeconfig := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"  - name: test-cluster\n" +
		"    cluster:\n" +
		"      server: " + apiServer.URL + "\n" +
		"contexts:\n" +
		"  - name: test-context\n" +
		"    context:\n" +
		"      cluster: test-cluster\n" +
		"      user: test-user\n" +
		"users:\n" +
		"  - name: test-user\n" +
		"    user:\n" +
		"      token: test-token\n" +
		"current-context: test-context\n"
	if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("deployment wait used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}

func testDoctorDryRunRollbackTracesHelmRollback(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_rollback_traces_helm_rollback", normalize.Apply(result.Combined))
}

func testDoctorRealRunRollbackFailureReportsHelmStderr(t *testing.T) {
	// A failed rollback carries helm's output, not "exit status 1".
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`case "$1" in`,
		`  status) printf '%s\n' 'NAME: team-devops' 'STATUS: deployed' ;;`,
		`  rollback) echo 'Error: release: "team-devops": release has no rollback candidate' >&2; exit 1 ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a failed rollback, got 0: %s", result.Combined)
	}
	golden.Equal(t, "doctor/real_run_rollback_failure_reports_helm_stderr", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsMemoryPressureAndTheSizingVerdict(t *testing.T) {
	// The resize verdict is reported, never applied: resizing rolls the runtime pod.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`case "$1" in`,
		`  status) printf '%s\n' 'NAME: team-devops' 'STATUS: deployed' ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	stubDoctorKubectlWithUsage(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Asserted by wording too, so a golden refresh dropping the section is caught.
	for _, want := range []string{
		"== Resources ==",
		"memory is at 100% of its 6144Mi limit (warns at 85%)",
		"memory.peak reached 100% of the limit (warns at 95%)",
		"the cgroup recorded 3 OOM kill(s)",
		"memory raise to 9216Mi from 6144Mi (3 oom kill(s) at 6144Mi, high confidence)",
		"cpu raise to 9 from 6 (9.85% of scheduling periods throttled (573996 of 5829923), high confidence)",
		"erun resize --tenant team --environment dev --memory 9216Mi --cpu 9",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in doctor's output, got:\n%s", want, result.Combined)
		}
	}
	if strings.Contains(result.Combined, "Running: ") {
		t.Fatalf("doctor must report the sizing verdict without applying it, got:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_memory_pressure_and_the_sizing_verdict", normalize.Apply(result.Combined))
}

func testDoctorDryRunClearPendingHelmTracesKubectlDelete(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--clear-pending-helm"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_clear_pending_helm_traces_kubectl_delete", normalize.Apply(result.Combined))
}

func testDoctorConflictingRecoveryFlagsError(t *testing.T) {
	// Applying both would roll the release back one revision too far.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--clear-pending-helm", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
	}
	golden.Equal(t, "doctor/conflicting_recovery_flags_error", normalize.Apply(result.Combined))
}

func testDoctorRealRunClusterUnreachableSkipsPodDependentSectionsOnceEstablished(t *testing.T) {
	// Once helm reports the API server unreachable, later sections must not re-probe.
	// kubectl is unstubbed, so any real probe fails the test.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithAWSAlias(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatusUnreachable(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_cluster_unreachable_skips_pod_dependent_sections_once_established", normalize.Apply(result.Combined))
}

func testDoctorRealRunStorageUnhealthyDiagnosticError(t *testing.T) {
	// A disk i/o error on wait degrades only the docker-storage check and skips the
	// prune. Real-run only: dry-run never executes the wait.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, `echo 'Error from server: disk i/o error' >&2; exit 1`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_storage_unhealthy_diagnostic_error", normalize.Apply(result.Combined))
}

func testDoctorRealRunUnrecognizedWaitFailureStillReportsKubectlStderr(t *testing.T) {
	// An unrecognized wait failure still carries kubectl's message.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, `echo 'Error from server: etcdserver: request timed out' >&2; exit 1`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_unrecognized_wait_failure_still_reports_kubectl_stderr", normalize.Apply(result.Combined))
}

func testDoctorRealRunUnrecognizedWaitFailureWithNoStderrReportsExitStatusPlainly(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, `exit 1`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_unrecognized_wait_failure_with_no_stderr_reports_exit_status_plainly", normalize.Apply(result.Combined))
}

func testDoctorRealRunNamespaceListedButApiFailingError(t *testing.T) {
	// wait says the namespace is missing while get namespaces lists it: API access
	// is broken. Real-run only.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectl(t, stubs, `echo 'Error from server (NotFound): namespaces "team-dev" not found' >&2; exit 1`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_namespace_listed_but_api_failing_error", normalize.Apply(result.Combined))
}

// stubDoctorKubectlWithUsage reports memory pinned at its limit with OOM kills and
// CPU throttling. The dind exec comes first; usage reads are told from the storage
// probe by their script.
func stubDoctorKubectlWithUsage(t *testing.T, stubsDir string) {
	t.Helper()
	runtimeLines := []string{
		"cgroup_type=cgroup2fs",
		"memory_current=6442450944",
		"memory_max=6442450944",
		"memory_peak=6442459136",
		"memory_oom_kill=3",
		"cpu_max=600000 100000",
		"cpu_usage_before=581511501",
		"cpu_usage_after=581611501",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"cpu_periods=5829923",
		"cpu_throttled_periods=573996",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		"disk_own_used_kb=44040192",
	}
	// The sidecar is a separate cgroup and reports its own pressure.
	dindLines := []string{
		"cgroup_type=cgroup2fs",
		"memory_current=1073741824",
		"memory_max=17179869184",
		"memory_peak=2147483648",
		"memory_oom_kill=0",
		"cpu_max=800000 100000",
		"cpu_usage_before=581511501",
		"cpu_usage_after=581611501",
		"cpu_periods=5829923",
		"cpu_throttled_periods=0",
	}
	script := []string{
		`case "$*" in`,
		`  *" -c erun-dind "*"memory_oom_kill"*)`,
	}
	for _, line := range dindLines {
		script = append(script, `    printf '%s\n' '`+line+`'`)
	}
	script = append(script, `    ;;`, `  *"memory_oom_kill"*)`)
	for _, line := range runtimeLines {
		script = append(script, `    printf '%s\n' '`+line+`'`)
	}
	script = append(script, `    ;;`, `  *"memory_oom_kill"*)`)
	for _, line := range runtimeLines {
		script = append(script, `    printf '%s\n' '`+line+`'`)
	}
	script = append(script,
		`    ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`esac`,
		`exit 0`,
	)
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", strings.Join(script, "\n"))
}

// stubDoctorHelmStatusUnreachable fails `helm status` with an unreachable-API-server error.
func stubDoctorHelmStatusUnreachable(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$1" in`,
		`  status)`,
		`    echo 'Error: kubernetes cluster unreachable: Get "https://198.51.100.10:6443/version?timeout=32s": dial tcp 198.51.100.10:6443: i/o timeout' >&2`,
		`    exit 1`,
		`    ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "helm", script)
}
