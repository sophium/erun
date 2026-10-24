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
	// An operator who flipped execution.modes.aws-sts to "library" in
	// config.yaml must see it reflected here, not just take it on faith.
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
	// Regression coverage for erun#2403: --dry-run withholds mutations,
	// not reads, so the helm/kubectl diagnosis reads must still run and
	// their sections must still print -- the same content a real run
	// would show -- instead of the diagnosis being silently suppressed.
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
	// Locks the dry-run/audit contract for kubectl-deployment-wait: the
	// kubectl trace lines must stay byte-identical to the
	// subprocess-mode golden (dry_run_prune_images_traces_dind_exec,
	// modulo the "Execution modes" line itself reporting the flipped
	// mode) even with execution.modes.kubectl-deployment-wait=library,
	// since traceAndWaitForRuntime only ever renders the trace line
	// (kubectlDeploymentWaitArgs) and never calls
	// libraryWaitForDeploymentAvailable on a dry run.
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
	// Proves the library path produces the same observable result as the
	// subprocess path (the runtime deployment reports Available, so the
	// doctor action proceeds) via a real client-go call against a fake API
	// server instead of shelling out to kubectl. The kubectl stub fails
	// loudly and distinctively only for the "wait" argv shape (every
	// other kubectl subcommand this doctor run needs -- pod listing,
	// dind exec -- still succeeds), so a fallback to the subprocess path
	// for the wait surfaces as a recognizable failure instead of
	// silently passing.
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
	// Exercises the deploy-recovery action: --rollback --dry-run must
	// trace the `helm rollback <release> --namespace --kube-context
	// --wait --timeout` command for the runtime release, without
	// mutating the cluster.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_rollback_traces_helm_rollback", normalize.Apply(result.Combined))
}

func testDoctorRealRunRollbackFailureReportsHelmStderr(t *testing.T) {
	// erun#1768: RunDeployRecovery's rollback branch always captured
	// helm's combined output, but runDeployRecoveryActions (the CLI
	// caller) used to discard it on failure and return the bare error
	// -- a real rollback failure read as a content-free "exit status
	// 1" with no explanation of why. It must instead carry helm's own
	// message.
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
	// The signal `erun usage` computes and doctor used to omit entirely: an
	// environment pinned at 100% of its memory limit with real OOM kills and
	// sustained CPU throttling. Doctor reported disk and nothing else, so the
	// environment that most needed attention read as a disk question. The
	// verdict is reported and never applied -- resizing rolls the runtime pod
	// -- so the run must name the explicit `erun resize` values and must not
	// run a resize.
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
	// The warning, the verdict and the next action are asserted by their own
	// wording as well as by the snapshot: a golden refresh that dropped the
	// section would still have to be reviewed against these.
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
	// Exercises the deploy-recovery action: --clear-pending-helm
	// --dry-run must trace the `kubectl delete secrets,configmaps -l
	// owner=helm,...,status in (pending-install,...)` command that
	// clears the stuck helm pending-operation lock.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--clear-pending-helm"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_clear_pending_helm_traces_kubectl_delete", normalize.Apply(result.Combined))
}

func testDoctorConflictingRecoveryFlagsError(t *testing.T) {
	// Clearing a pending lock and rolling back are alternative
	// recoveries; passing both must fail fast with a clear error
	// rather than running both (which would step the release back a
	// revision too far).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--clear-pending-helm", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
	}
	golden.Equal(t, "doctor/conflicting_recovery_flags_error", normalize.Apply(result.Combined))
}

func testDoctorRealRunClusterUnreachableSkipsPodDependentSectionsOnceEstablished(t *testing.T) {
	// Once the helm release status read confirms the Kubernetes API
	// server itself is unreachable, doctor must not rediscover that fact
	// in the Pods, Host AWS credentials, Git push
	// access, or Docker storage sections -- before the fix each paid its
	// own multi-minute kubectl timeout to relearn what the helm read
	// already established (~8 minutes and 17 klog frames across the four
	// sections in the reported run). kubectl is deliberately left
	// unstubbed (absent from fixture.StubEnv below): if any of the four
	// sections still probes for real, erun.Run's own
	// "executable file not found" detection fails this test outright.
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
	// kubectl wait fails with a disk i/o error: doctor must fold the
	// stderr into the storage-unhealthy diagnostic
	// (normalizeDoctorKubectlError → doctorKubectlDiagnostic's
	// unhealthy-storage arm) and degrade the docker-storage check instead
	// of aborting the whole command (#1325) — the helm/pod diagnosis
	// above already reported the cluster is reachable, so this failure is
	// specific to the docker inspection alone, and the requested prune is
	// skipped for the same reason rather than attempted and failing too.
	// Only reachable in a real run — dry-run never executes the wait.
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
	// erun#1768: kubectl wait fails with a cause doctorKubectlDiagnostic
	// does not recognize (neither the disk-i/o-error nor the
	// namespace-lookup-failed shape above matches). Before the fix,
	// normalizeDoctorKubectlError's unrecognized-cause branch discarded
	// the captured stderr and returned the bare wrapped error, so the
	// degraded report read "could not read: exit status 1" with no
	// explanation. It must instead carry kubectl's own message.
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
	// erun#1768's empty-stderr case: when kubectl wait fails with
	// nothing captured on stderr, normalizeDoctorKubectlError has
	// nothing to add, so the degraded report must still name the exit
	// status plainly rather than emitting a blank fragment.
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
	// kubectl wait reports the runtime namespace as not found while
	// `kubectl get namespaces` lists it — the split-brain failure
	// mode where the namespace exists but direct API access is
	// broken. Covers doctorNamespaceLookupFailed +
	// doctorNamespaceIsListed and the second diagnostic arm of
	// doctorKubectlDiagnostic; the probe-then-diagnose sequence only
	// runs on a real wait failure. Degrades the docker-storage check
	// rather than aborting the command (#1325), same as the storage
	// scenario above.
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

// stubDoctorKubectlWithUsage answers doctor's read-only surfaces and both of
// the resources section's cgroup reads with the figures four erun fleet
// environments actually reported: memory pinned at 100% of a 6144Mi limit with
// three recorded OOM kills, and 9.8% of scheduling periods throttled at a
// 6-core quota.
//
// The exec arms are ordered because doctor dispatches more than one exec: the
// dind one first (the sidecar's own reading and doctor's docker-storage probe
// both use -c erun-dind), then the runtime container's. The usage reads are
// told apart from the docker-storage probe by the reading script in the exec
// argv -- memory_oom_kill for the one, df -h /var/lib/docker for the other.
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
	// The sidecar reads healthy: it is a separate cgroup, and the section must
	// report its own pressure rather than mirroring the runtime container's.
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

// stubDoctorHelmStatusUnreachable stubs `helm status` to fail with the
// unreachable-API-server error a real cluster reports, matching the reported
// run's exact wording. No other helm command is expected to run once this
// fails.
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
