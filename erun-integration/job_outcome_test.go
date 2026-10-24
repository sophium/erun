package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testJobAnAbandonedJobReadsAsUnknownNeverAsSuccess(t *testing.T) {
	// A record whose supervisor is gone without an outcome — what a replaced
	// runtime pod leaves behind. Seeded directly, because killing a supervisor
	// mid-run would race the outcome it is about to write, and the state under
	// test is the record, not the kill. A pid of 1 that erun is not is a
	// process the record can be reconciled against; the seeded pid below is one
	// that cannot exist.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "stranded"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	record := `{
  "id": "stranded",
  "name": "overnight build",
  "state": "running",
  "pid": 2147483646,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-stranded"
}
`
	if err := os.WriteFile(filepath.Join(dir, "stranded.json"), []byte(record), 0o644); err != nil {
		t.Fatalf("seed job record: %v", err)
	}
	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "stranded"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if status.ExitCode != 0 {
		t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "stranded", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if await.ExitCode != 125 {
		t.Fatalf("expected the unknown-outcome exit code 125, distinct from both 0 and 124, got %d:\n%s", await.ExitCode, await.Combined)
	}
	golden.Equal(t, "job/an_abandoned_job_reads_as_unknown_never_as_success", normalize.Apply(status.Combined+await.Combined))

	// The demotion is persisted, so every later read gives the same definite
	// answer instead of re-deciding it.
	body, err := os.ReadFile(filepath.Join(dir, "stranded.json"))
	if err != nil {
		t.Fatalf("read reconciled record: %v", err)
	}
	if !strings.Contains(string(body), `"state": "unknown"`) {
		t.Errorf("expected the reconciled record to persist the unknown state, got:\n%s", body)
	}
}

// seedRestartedJobOnThisPod records a job "restarted" as still running under
// a dead pid on this host's own hostname, which it returns.
func seedRestartedJobOnThisPod(t *testing.T, setup env.Setup) string {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "restarted"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	record := fmt.Sprintf(`{
  "id": "restarted",
  "name": "overnight build",
  "state": "running",
  "pid": 2147483645,
  "hostname": %q,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-restarted"
}
`, hostname)
	if err := os.WriteFile(filepath.Join(dir, "restarted.json"), []byte(record), 0o644); err != nil {
		t.Fatalf("seed job record: %v", err)
	}
	return hostname
}

func testJobAnAbandonedJobOnTheSamePodNamesAContainerRestartViaKubectlSubprocess(t *testing.T) {
	// A stranded job whose recorded hostname matches this pod's own
	// hostname rules out a pod replacement, so reconciliation asks
	// Kubernetes whether the runtime container itself restarted
	// (jobSupervisorContainerRestart, kubectl-pod-get execution mode).
	// The kubectl stub answers `get pod <hostname> -o json` with a
	// terminated erun-devops container, and the reported reason must name
	// the real cause instead of falling back to "could not be
	// determined".
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedRestartedJobOnThisPod(t, setup)

	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get pod "*) printf '%s\n' '{"status":{"containerStatuses":[{"name":"erun-devops","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"2026-01-01T00:05:00Z"}}}]}}' ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...))

	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "restarted"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if status.ExitCode != 0 {
		t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "restarted", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 125 {
		t.Fatalf("expected the unknown-outcome exit code 125, got %d:\n%s", await.ExitCode, await.Combined)
	}
	golden.Equal(t, "job/an_abandoned_job_on_the_same_pod_names_a_container_restart_via_kubectl_subprocess", normalize.Apply(status.Combined+await.Combined))
}

func testJobAnAbandonedJobOnTheSamePodNamesAContainerRestartViaKubectlPodGetLibraryExecutionMode(t *testing.T) {
	// Proves the library path (kubectl-pod-get=library) produces the same
	// observable result as the subprocess path above -- a real client-go
	// call against a fake API server instead of shelling out to kubectl.
	// The kubectl stub fails loudly and distinctively only for a "get
	// pod" argv, so a fallback to the subprocess path surfaces as a
	// recognizable failure instead of silently passing.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-pod-get", "library")
	hostname := seedRestartedJobOnThisPod(t, setup)

	var apiHits atomic.Int64
	podPath := "/api/v1/namespaces/team-dev/pods/" + hostname
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != podPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":{"containerStatuses":[{"name":"erun-devops","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"2026-01-01T00:05:00Z"}}}]}}`)
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
		"      namespace: team-dev\n" +
		"users:\n" +
		"  - name: test-user\n" +
		"    user:\n" +
		"      token: test-token\n" +
		"current-context: test-context\n"
	if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get pod "*) printf '%s\n' "fell through to the kubectl subprocess for the container restart check" >&2; exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...))

	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "restarted"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if status.ExitCode != 0 {
		t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
	}
	if strings.Contains(status.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("container restart check used the kubectl subprocess despite library execution mode:\n%s", status.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
	if !strings.Contains(status.Combined, "OOMKilled") {
		t.Fatalf("expected the library path to report the same OOMKilled restart the subprocess path does:\n%s", status.Combined)
	}
}

func testJobAJobThatBackgroundsWorkAndExitsReadsAsAbandonedNotSuccess(t *testing.T) {
	// The supervisor waits on its immediate child only; a child that
	// backgrounds a grandchild and exits 0 would otherwise read as a clean
	// success while the backgrounded work runs on unsupervised. Redirecting
	// the grandchild's output away from the supervisor's captured pipe is
	// what reproduces the shape -- a grandchild inheriting that pipe would
	// block the immediate child's own exit instead.
	if runtime.GOOS == "windows" {
		t.Skip("process-group survivor detection is POSIX-only; see erun-common/job_process_windows.go")
	}
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	backgroundLog := filepath.Join(setup.Cwd, "background.log")
	envVars := jobStubEnv(t, setup, "sleep 5 </dev/null >'"+backgroundLog+"' 2>&1 &\nexit 0")

	start := startJob(t, setup, envVars, "gate", "--", "work")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}

	status := waitForJobToLeaveRunning(t, setup, envVars, "gate")
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "gate", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 1 {
		t.Fatalf("expected an abandoned job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
	}
	// : a raw exitCode of 0 sitting beside state "abandoned" is
	// exactly the false-success shape a caller reading only exitCode would
	// miss; succeeded must say so explicitly rather than leaving it to be
	// re-derived from state.
	statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "gate", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	var payload struct {
		State     string `json:"state"`
		ExitCode  *int   `json:"exitCode"`
		Succeeded bool   `json:"succeeded"`
	}
	if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
		t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
	}
	if payload.State != "abandoned" || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.Succeeded {
		t.Fatalf("expected an abandoned job with exitCode 0 to report succeeded=false, got %+v", payload)
	}
	golden.Equal(t, "job/a_job_that_backgrounds_work_and_exits_reads_as_abandoned_not_success", normalize.Apply(status.Combined+await.Combined))
}

func testJobAJobStartedOnADifferentPodReadsBackAsPodReplacedNotAGuess(t *testing.T) {
	// The supervisor stamps the pod's hostname at start, so a record
	// read back from a different hostname is definitive proof of pod
	// replacement, not the older "most likely" guess. Seeded directly for
	// the same reason as the sibling "abandoned" scenario: the state under
	// test is the record's hostname mismatch, not how a real pod eviction
	// would produce one. hostname is a value this suite's own host can never
	// report, so the mismatch is guaranteed.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "evicted"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	record := `{
  "id": "evicted",
  "name": "overnight release",
  "state": "running",
  "pid": 2147483646,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-evicted",
  "hostname": "team-devops-original-pod-58f9c7d9b6-abcde"
}
`
	if err := os.WriteFile(filepath.Join(dir, "evicted.json"), []byte(record), 0o644); err != nil {
		t.Fatalf("seed job record: %v", err)
	}
	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "evicted", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if status.ExitCode != 0 {
		t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
	}
	var payload struct {
		State             string `json:"state"`
		UnknownReasonKind string `json:"unknownReasonKind"`
		Reason            string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(status.Stdout), &payload); err != nil {
		t.Fatalf("parse job status JSON: %v\n%s", err, status.Stdout)
	}
	if payload.State != "unknown" {
		t.Fatalf("state = %q, want unknown", payload.State)
	}
	if payload.UnknownReasonKind != "pod-replaced" {
		t.Fatalf("unknownReasonKind = %q, want pod-replaced", payload.UnknownReasonKind)
	}
	if strings.Contains(payload.Reason, "most likely") {
		t.Fatalf("expected a definite reason (hostname mismatch), not a guess, got: %s", payload.Reason)
	}
	if !strings.Contains(payload.Reason, "team-devops-original-pod-58f9c7d9b6-abcde") {
		t.Fatalf("expected the reason to name the pod the job started on, got: %s", payload.Reason)
	}
}
